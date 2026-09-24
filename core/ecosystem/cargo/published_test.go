package cargo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestPublishedAsksTheCratesAPI(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		want    bool
		wantErr bool
	}{
		{"the version is there", http.StatusOK, true, false},
		{"it isn't", http.StatusNotFound, false, false},
		{"the registry failed", http.StatusInternalServerError, false, true},
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
			}))
			defer srv.Close()
			resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{
				Package:       plugin.Package{Name: "acme-lib", Version: "1.2.0"},
				PackageSource: srv.URL,
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
