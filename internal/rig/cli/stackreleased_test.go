package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestStackReleasedEvidence(t *testing.T) {
	sha := strings.Repeat("a", 40)
	release := strings.Repeat("b", 40)
	for _, tc := range []struct{ name, commit, status, want string }{
		{"released", release, "ahead", ""}, {"missing", "", "ahead", "can't tell"}, {"pending", release, "behind", "not yet released"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, ".nuspec"):
					fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/lib" commit="%s" /></metadata></package>`, tc.commit)
				case strings.Contains(r.URL.Path, "/compare/"):
					fmt.Fprintf(w, `{"status":%q}`, tc.status)
				case strings.HasSuffix(r.URL.Path, "/pulls/1"):
					fmt.Fprintf(w, `{"number":1,"merged_at":"today","merge_commit_sha":%q}`, sha)
				default:
					fmt.Fprint(w, `[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/lib"}}}]`)
				}
			}))
			defer srv.Close()
			m := &stackManifest{Repos: map[string]*stackRepo{"lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"lib": sha}, Proposals: map[string]map[string]stackProposal{"lib": {"topic": {Branch: "stack/topic"}}}}
			v, err := stackReleaseVersion(context.Background(), stackDepsAPI{base: srv.URL, client: srv.Client()}, m, "lib", "Acme.Lib", "1.2.3", srv.URL)
			if tc.want == "" {
				if err != nil || v != "1.2.3" {
					t.Fatalf("%s %v", v, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestStackReleasedRewrite(t *testing.T) {
	var cases []struct {
		Name, In, Want string
		Refuse         bool
	}
	data, err := os.ReadFile("testdata/stackreleased-rewrite.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, reason := stackReleaseRewrite([]byte(tc.In), "STACK_X")
			if tc.Refuse {
				if reason == "" || !bytes.Equal(got, []byte(tc.In)) {
					t.Fatalf("unsafe rewrite %q %s", got, reason)
				}
			} else if reason != "" || string(got) != tc.Want {
				t.Fatalf("%q %s", got, reason)
			}
		})
	}
}

func TestStackReleasedXML(t *testing.T) {
	in := []byte("\xef\xbb\xbf<Project>\r\n<PackageVersion Include=\"Acme.Lib\" Version=\"1.0.0\" />\r\n</Project>")
	got, changed, _ := stackReleaseXML(in, "Acme.Lib", "2.0.0")
	if !changed || string(got) != strings.Replace(string(in), "1.0.0", "2.0.0", 1) {
		t.Fatalf("%q", got)
	}
	in = []byte(`<Project><PackageReference Include="Acme.Lib" Version="1.*" /></Project>`)
	got, changed, _ = stackReleaseXML(in, "Acme.Lib", "2.0.0")
	if changed || !bytes.Equal(in, got) {
		t.Fatal("floating version modified")
	}
}

func TestStackReleasedWarning(t *testing.T) {
	in := []byte(`<Project><StackOnlyWarning Include="ACME0001" /><StackOnlyWarning Include="ACME0002" /></Project>`)
	got, _, notes := stackReleaseXML(in, "Acme.Lib", "2.0.0", "ACME0001")
	if strings.Contains(string(got), "ACME0001") || !strings.Contains(string(got), "ACME0002") || len(notes) != 1 {
		t.Fatalf("%s %v", got, notes)
	}
}

func TestStackReleasedPartialProposals(t *testing.T) {
	first, second, cursor, release := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, ".nuspec"):
			fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/lib" commit="%s" /></metadata></package>`, release)
		case strings.Contains(r.URL.Path, "/compare/"):
			status := "ahead"
			if strings.Contains(r.URL.Path, second) {
				status = "behind"
			}
			fmt.Fprintf(w, `{"status":%q}`, status)
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, first)
		case strings.HasSuffix(r.URL.Path, "/pulls/2"):
			fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, second)
		default:
			branch := "stack/one"
			n := 1
			if strings.Contains(r.URL.Query().Get("head"), "stack/two") {
				branch = "stack/two"
				n = 2
			}
			fmt.Fprintf(w, `[{"number":%d,"head":{"ref":%q,"repo":{"full_name":"you/lib"}}}]`, n, branch)
		}
	}))
	defer srv.Close()
	m := &stackManifest{Repos: map[string]*stackRepo{"lib": {Upstream: "github.com/acme/lib", Fork: "github.com/you/lib"}}, LastSync: map[string]string{"lib": cursor}, Proposals: map[string]map[string]stackProposal{"lib": {"one": {Branch: "stack/one"}, "two": {Branch: "stack/two"}}}}
	_, err := stackReleaseVersion(context.Background(), stackDepsAPI{base: srv.URL, client: srv.Client()}, m, "lib", "Acme.Lib", "2.0.0", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "not yet released") {
		t.Fatalf("partial release accepted: %v", err)
	}
}

func TestStackReleasedConditionalAndDowngrade(t *testing.T) {
	for _, in := range []string{`<Project><ItemGroup Condition="'$(Foo)'=='true'"><PackageVersion Include="Acme.Lib" Version="1.0.0" /></ItemGroup></Project>`, `<Project><PackageVersion Include="Acme.Lib" Version="3.0.0" /></Project>`} {
		got, changed, notes := stackReleaseXML([]byte(in), "Acme.Lib", "2.0.0")
		if changed || string(got) != in || len(notes) == 0 {
			t.Fatalf("unsafe pin: %s %v", got, notes)
		}
	}
}

func TestStackReleasedXMLQuotedAttribute(t *testing.T) {
	in := `<Project><PackageVersion Include="Acme.Lib" Label='Version="fake"' Version="1.0.0" /></Project>`
	got, changed, _ := stackReleaseXML([]byte(in), "Acme.Lib", "2.0.0")
	if !changed || string(got) != strings.Replace(in, `Version="1.0.0"`, `Version="2.0.0"`, 1) {
		t.Fatalf("%s", got)
	}
}
