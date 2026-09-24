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
		{"a four-part version with a zero", "1.2.0.0", 200, `{"versions":["1.2.0"]}`, true, false},
		{"leading zeros", "01.02.0", 200, `{"versions":["1.2.0"]}`, true, false},
		{"a fourth number that isn't zero", "1.2.0.1", 200, `{"versions":["1.2.0"]}`, false, false},
		{"a 200 without the list", "1.2.0", 200, `{}`, false, true},
		{"a 200 with an empty list", "1.2.0", 200, `{"versions":[]}`, false, true},
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

// Credentials in a feed URL never reach an error message.
func TestFeedErrorsRedactCredentials(t *testing.T) {
	// Nothing listens on port 1, so the request fails.
	_, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
		Package:       plugin.Package{Name: "Acme.Lib", Version: "1.0.0"},
		PackageSource: "http://user:s3cret@127.0.0.1:1/v3/index.json",
	})
	if err == nil {
		t.Fatal("want an error from an unreachable feed")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaks the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error should still name the feed: %v", err)
	}
}

// A packed .nupkg is pushed as it is: nothing is packed.
func TestPublishPushesAPackedNupkgWithoutPacking(t *testing.T) {
	t.Setenv("NUGET_API_KEY", "")
	wasPack, wasPush := packRunner, pushRunner
	t.Cleanup(func() { packRunner, pushRunner = wasPack, wasPush })
	packRunner = func(context.Context, string, string, ...string) (string, string, error) {
		t.Error("dotnet pack ran for a packed release")
		return "", "", nil
	}
	var pushed []string
	pushRunner = func(_ context.Context, _ string, _ string, args ...string) (string, string, error) {
		pushed = args
		return "", "", nil
	}
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot:     "/repo",
		Package:      plugin.Package{Name: "Acme.Lib", Version: "1.2.0", ManifestPath: "src/Acme.Lib/Acme.Lib.csproj"},
		ArtifactPath: "/pack/packages/Acme.Lib.1.2.0.nupkg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed) < 3 || pushed[0] != "nuget" || pushed[1] != "push" || pushed[2] != "/pack/packages/Acme.Lib.1.2.0.nupkg" {
		t.Errorf("push args = %v", pushed)
	}
}
