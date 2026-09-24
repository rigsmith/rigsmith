package cargo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestPublishedAsksTheCratesAPI(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{"the version is there", http.StatusOK, `{"version":{"num":"1.2.0"}}`, true, false},
		{"it isn't", http.StatusNotFound, ``, false, false},
		{"the registry failed", http.StatusInternalServerError, ``, false, true},
		{"a 200 that isn't the answer", http.StatusOK, `<html>sign in</html>`, false, true},
		{"a 200 for another version", http.StatusOK, `{"version":{"num":"1.1.0"}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/crates/acme-lib/1.2.0" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.Header.Get("User-Agent") == "" {
					t.Error("crates.io refuses requests without a User-Agent")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			was := cratesIOBase
			cratesIOBase = srv.URL
			defer func() { cratesIOBase = was }()
			resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
				Package: plugin.Package{Name: "acme-lib", Version: "1.2.0"},
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

func TestPublishedUnpublishableCrateHasNoRegistry(t *testing.T) {
	resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
		Package: plugin.Package{Name: "acme-app", Version: "1.0.0", Private: true},
	})
	if err != nil || !resp.NoRegistry {
		t.Errorf("resp = %+v, err = %v, want NoRegistry", resp, err)
	}
}

// Only crates.io is asked: a named or URL registry's answer can't be trusted
// to mean what crates.io's does, so it's an error rather than a guess.
func TestPublishedRefusesOtherRegistries(t *testing.T) {
	for _, src := range []string{"my-registry", "https://registry.example.com"} {
		_, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
			Package:       plugin.Package{Name: "acme-lib", Version: "1.2.0"},
			PackageSource: src,
		})
		if err == nil || !strings.Contains(err.Error(), "only crates.io") {
			t.Errorf("source %q: err = %v, want the crates.io-only refusal", src, err)
		}
	}
	for _, src := range []string{"", "crates.io", "crates"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
		was := cratesIOBase
		cratesIOBase = srv.URL
		_, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
			Package:       plugin.Package{Name: "acme-lib", Version: "1.2.0"},
			PackageSource: src,
		})
		cratesIOBase = was
		srv.Close()
		if err != nil {
			t.Errorf("source %q means crates.io: err = %v", src, err)
		}
	}
}

// Cargo publishes from source only: a prebuilt crate is refused, not rebuilt.
func TestPublishRefusesAPrebuiltCrate(t *testing.T) {
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		Package:      plugin.Package{Name: "acme-lib", Version: "1.2.0"},
		ArtifactPath: "/pack/packages/acme-lib-1.2.0.crate",
	})
	if err == nil || !strings.Contains(err.Error(), "prebuilt crate") {
		t.Errorf("err = %v, want the prebuilt-crate refusal", err)
	}
}
