package dotnet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// feed serves a v3 service index at /index.json, and the flat container's
// version list for Acme.Lib (as the lowercased id) with the given status.
func feed(t *testing.T, status int, versions string) string {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			fmt.Fprintf(w, `{"resources":[{"@id":"%s/search","@type":"SearchQueryService"},{"@id":"%s/flat","@type":"PackageBaseAddress/3.0.0"}]}`, srv.URL, srv.URL)
		case "/flat/acme.lib/index.json":
			w.WriteHeader(status)
			fmt.Fprint(w, versions)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/index.json"
}

func TestPublishedReadsTheFlatContainer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  string
		status   int
		versions string
		want     bool
		wantErr  bool
	}{
		{"the version is there", "1.2.0", 200, `{"versions":["1.1.0","1.2.0"]}`, true, false},
		{"case and build metadata don't matter", "1.2.0-Beta.1+abc", 200, `{"versions":["1.2.0-beta.1"]}`, true, false},
		{"other versions only", "1.3.0", 200, `{"versions":["1.2.0"]}`, false, false},
		{"no such package", "1.2.0", 404, ``, false, false},
		{"the feed failed", "1.2.0", 500, `oops`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := feed(t, tc.status, tc.versions)
			resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
				Package:       plugin.Package{Name: "Acme.Lib", Version: tc.version},
				PackageSource: source,
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if resp.Published != tc.want {
				t.Errorf("Published = %v, want %v", resp.Published, tc.want)
			}
		})
	}
}

func TestPackageBaseAddress(t *testing.T) {
	for _, source := range []string{"", "nuget", "NuGet.org"} {
		if got, err := packageBaseAddress(context.Background(), source); err != nil || got != nugetOrgBase {
			t.Errorf("packageBaseAddress(%q) = %q, %v", source, got, err)
		}
	}
	// A NuGet.config source name can't be resolved here: say so, and how to fix it.
	if _, err := packageBaseAddress(context.Background(), "my-feed"); err == nil || !strings.Contains(err.Error(), "service index URL") {
		t.Errorf("packageBaseAddress(my-feed) error = %v, want the named-source explanation", err)
	}
}
