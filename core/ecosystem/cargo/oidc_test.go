package cargo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestCratesRegistryBase(t *testing.T) {
	cases := map[string]string{
		"":                  "https://crates.io",
		"crates.io":         "https://crates.io",
		"crates":            "https://crates.io",
		"https://crates.io": "https://crates.io",
		"https://my.reg/":   "https://my.reg",
	}
	for in, want := range cases {
		if got := cratesRegistryBase(in); got != want {
			t.Errorf("cratesRegistryBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCratesAudience(t *testing.T) {
	if got := cratesAudience("https://crates.io"); got != "crates.io" {
		t.Errorf("audience = %q, want crates.io", got)
	}
	if got := cratesAudience("https://my.reg/"); got != "my.reg" {
		t.Errorf("audience = %q, want my.reg", got)
	}
}

func TestExchangeCratesToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/api/v1/trusted_publishing/tokens") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		if in["jwt"] != "id-jwt" {
			t.Errorf("jwt body = %q", in["jwt"])
		}
		_, _ = w.Write([]byte(`{"token":"crates-tok"}`))
	}))
	defer srv.Close()

	got, err := exchangeCratesToken(context.Background(), srv.URL, "id-jwt")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got != "crates-tok" {
		t.Errorf("token = %q", got)
	}
}

func TestExchangeCratesToken_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"no trusted publisher configured"}]}`))
	}))
	defer srv.Close()

	_, err := exchangeCratesToken(context.Background(), srv.URL, "id-jwt")
	if err == nil {
		t.Fatal("want error on 403")
	}
	if !strings.Contains(err.Error(), "no trusted publisher configured") {
		t.Errorf("error missing registry detail: %v", err)
	}
}

func TestCratesOIDCToken_EndToEndAndRevoke(t *testing.T) {
	var mu sync.Mutex
	revoked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/idtoken"):
			_, _ = w.Write([]byte(`{"value":"minted-jwt"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/trusted_publishing/tokens"):
			if got := func() string {
				b, _ := io.ReadAll(r.Body)
				var m map[string]string
				_ = json.Unmarshal(b, &m)
				return m["jwt"]
			}(); got != "minted-jwt" {
				t.Errorf("exchange jwt = %q", got)
			}
			_, _ = w.Write([]byte(`{"token":"final-crates-token"}`))
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/trusted_publishing/tokens"):
			if r.Header.Get("Authorization") != "final-crates-token" {
				t.Errorf("revoke auth = %q", r.Header.Get("Authorization"))
			}
			mu.Lock()
			revoked = true
			mu.Unlock()
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/idtoken")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
	t.Setenv("NPM_ID_TOKEN", "")

	token, revoke, err := cratesOIDCToken(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("cratesOIDCToken: %v", err)
	}
	if token != "final-crates-token" {
		t.Errorf("token = %q", token)
	}
	revoke()
	mu.Lock()
	defer mu.Unlock()
	if !revoked {
		t.Error("revoke() did not DELETE the token")
	}
}

func TestReleaseInit_OIDCMakesTokenOptional(t *testing.T) {
	a := &Adapter{}
	on, err := a.ReleaseInit(context.Background(), plugin.ReleaseInitRequest{OIDC: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(on.Tokens) != 0 {
		t.Errorf("OIDC on: want no required tokens, got %+v", on.Tokens)
	}
	if !strings.Contains(strings.Join(on.Notes, "\n"), "Trusted Publish") {
		t.Errorf("OIDC on: missing setup note: %v", on.Notes)
	}

	off, err := a.ReleaseInit(context.Background(), plugin.ReleaseInitRequest{OIDC: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Tokens) != 1 || off.Tokens[0].EnvVar != "CARGO_REGISTRY_TOKEN" {
		t.Errorf("OIDC off: want CARGO_REGISTRY_TOKEN, got %+v", off.Tokens)
	}
}
