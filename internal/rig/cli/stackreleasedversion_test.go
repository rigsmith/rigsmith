package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestStackReleasedNugetCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"2.0.0.2", "2.0.0.1", 1}, {"2.0.0.1", "2.0.0.2", -1},
		{"1.0", "1.0.0.0", 0}, {"1.0.0", "1.0.0.0", 0},
		{"1.0.0.1-alpha", "1.0.0", 1}, {"1.0.0.1-alpha", "1.0.0.1", -1},
		{"1.0-beta.2", "1.0-beta.10", -1}, {"1.0-beta.10", "1.0-beta.2", 1},
		{"1.0-beta", "1.0-BETA", 0}, {"1.0-2", "1.0-alpha", -1},
		{"1.0-beta.1", "1.0-beta", 1}, {"1.0+build.1", "1.0+build.2", 0},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			a, ok := stackParseNugetVersion(tc.a)
			if !ok {
				t.Fatal(tc.a)
			}
			b, ok := stackParseNugetVersion(tc.b)
			if !ok {
				t.Fatal(tc.b)
			}
			if got := stackCompareNuget(a, b); got != tc.want {
				t.Fatalf("compare %s %s = %d want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
	for _, s := range []string{"", "1", "1.2.3.4.5", "2.*", "[1.0]", "1.0.2147483648", "1.0-01", "1.0-a..b", "1.0+", "1.0-alpha+bad+metadata", "1.0.-1", "v1.0", "1.0-ä", "1.0-999999999999999999999999", "1.0-2147483648"} {
		if _, ok := stackParseNugetVersion(s); ok {
			t.Fatalf("unsupported version accepted: %s", s)
		}
	}
}

func TestStackReleasedNugetDeliveryRevisions(t *testing.T) {
	for _, tc := range []struct {
		minimum, required string
		want              bool
	}{
		{"2.0.0.1", "2.0.0.2", false}, {"[2.0.0.1,3.0)", "2.0.0.2", false},
		{"[2.0.0.2,3.0)", "2.0.0.1", true}, {"[2.0.0.2,2.0.0.1]", "2.0.0.1", false},
		{"1.0", "1.0.0.0", true}, {"1.0.0.0", "1.0", true},
		{"1.0-beta.2", "1.0-beta.10", false}, {"1.0-beta.10", "1.0-beta.2", true},
		{"1.0-beta", "1.0", false}, {"[2.0.0.2]", "2.0.0.2", true},
		{"1.0-01", "1.0", false}, {"2.0.0.2", "2.0.0.2147483648", false},
	} {
		d := stackReleaseDependencies{Direct: []stackReleaseDependency{{ID: "Acme.Core", Version: tc.minimum}}}
		if got := d.delivers("Acme.Core", tc.required); got != tc.want {
			t.Fatalf("%s delivers %s = %v", tc.minimum, tc.required, got)
		}
	}
}

func TestStackReleasedNugetBumpRevisions(t *testing.T) {
	for _, tc := range []struct {
		old, next string
		want      bool
	}{
		{"2.0.0.2", "2.0.0.1", false}, {"2.0.0.1", "2.0.0.2", true},
		{"1.0", "1.0.0.0", true}, {"1.0.0.0", "1.0", true},
		{"1.0-beta.10", "1.0-beta.2", false}, {"1.0-beta.2", "1.0-beta.10", true},
		{"1.0", "1.0-beta", false}, {"1.0", "1.0.0.2147483648", false},
	} {
		in := fmt.Sprintf(`<Project><PackageVersion Include="Acme.Core" Version="%s" /></Project>`, tc.old)
		out, changed, notes := stackReleaseXML([]byte(in), "Acme.Core", tc.next)
		if changed != tc.want {
			t.Fatalf("bump %s -> %s: changed %v, notes %v", tc.old, tc.next, changed, notes)
		}
		if !tc.want && string(out) != in {
			t.Fatal("unsafe bump changed file")
		}
	}
}

func TestStackReleasedNugetCandidates(t *testing.T) {
	got, err := stackReleaseCandidates([]string{"2.0.0.2", "1.0", "2.0.0.10", "2.0.0.1", "2.0.0.0", "2.0.0", "3.0-beta"})
	want := "2.0.0.10,2.0.0.2,2.0.0.1,2.0.0,1.0.0"
	if err != nil || strings.Join(got, ",") != want {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := stackReleaseCandidates([]string{"2.0.0.999999999999999999999"}); err == nil {
		t.Fatal("unknown index version accepted")
	}
}

func TestStackReleasedNugetEvidenceRevision(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, tc := range []struct{ requested, normalized string }{{"2.0.0.2", "2.0.0.2"}, {"1.0", "1.0.0"}, {"1.0.0.0", "1.0.0"}} {
		t.Run(tc.requested, func(t *testing.T) {
			nuspecs := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, ".nuspec"):
					nuspecs++
					if r.URL.Path != "/acme.core/"+tc.normalized+"/acme.core.nuspec" {
						t.Errorf("wrong package version requested: %s", r.URL.Path)
					}
					fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/core" commit="%s" /></metadata></package>`, sha)
				case strings.Contains(r.URL.Path, "/compare/"):
					fmt.Fprint(w, `{"status":"identical"}`)
				case strings.HasSuffix(r.URL.Path, "/pulls/1"):
					fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, sha)
				default:
					fmt.Fprint(w, `[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/core"}}}]`)
				}
			}))
			defer srv.Close()
			m := &stackManifest{Repos: map[string]*stackRepo{"core": {Upstream: "github.com/acme/core", Fork: "github.com/you/core"}}, LastSync: map[string]string{"core": sha}, Proposals: map[string]map[string]stackProposal{"core": {"topic": {Branch: "stack/topic"}}}}
			got, err := stackReleaseVersion(context.Background(), stackDepsAPI{base: srv.URL, client: srv.Client()}, m, "core", "Acme.Core", tc.requested, srv.URL)
			if err != nil || got != tc.normalized || nuspecs != 1 {
				t.Fatalf("%s %v requests=%d", got, err, nuspecs)
			}
		})
	}
}

func TestStackReleasedExactArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"app"}, {"app", "core"}, {"app", "core", "extra"}} {
		c := newStackReleasedCmd()
		c.SetArgs(args)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		called := false
		c.RunE = func(*cobra.Command, []string) error { called = true; return nil }
		err := c.Execute()
		if len(args) == 2 {
			if err != nil || !called {
				t.Fatalf("valid args rejected: %v", err)
			}
		} else if err == nil || called {
			t.Fatalf("invalid args reached RunE: %v (%v)", args, err)
		}
	}
}
