package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNpmRegistryBase(t *testing.T) {
	cases := map[string]string{
		"":                             "https://registry.npmjs.org",
		"npm":                          "https://registry.npmjs.org",
		"https://my.registry/":         "https://my.registry",
		"https://my.registry/api/npm/": "https://my.registry/api/npm",
	}
	for in, want := range cases {
		if got := npmRegistryBase(in); got != want {
			t.Errorf("npmRegistryBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNpmVersionAtLeast(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"11.5.1", true},
		{"11.5.2", true},
		{"12.0.0", true},
		{"11.6.0", true},
		{"11.5.0", false},
		{"11.4.9", false},
		{"10.8.2", false},
		{"11.5.1-beta.1", true}, // suffix stripped → 11.5.1
		{"11.5", false},         // too few parts
		{"garbage", false},
	}
	for _, tc := range cases {
		if got := npmVersionAtLeast(tc.v, 11, 5, 1); got != tc.want {
			t.Errorf("npmVersionAtLeast(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestExchangeNpmToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		// Scoped name: slash encoded, @ kept (npm registry convention). Check
		// the raw/escaped path — r.URL.Path is already percent-decoded.
		if !strings.HasSuffix(r.URL.EscapedPath(), "/-/npm/v1/oidc/token/exchange/package/@acme%2Fwidget") {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer id-tok" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"token":"publish-tok-xyz"}`))
	}))
	defer srv.Close()

	got, err := exchangeNpmToken(context.Background(), "id-tok", "@acme/widget", srv.URL)
	if err != nil {
		t.Fatalf("exchangeNpmToken: %v", err)
	}
	if got != "publish-tok-xyz" {
		t.Errorf("token = %q, want publish-tok-xyz", got)
	}
}

func TestExchangeNpmToken_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"package not configured for trusted publishing"}`))
	}))
	defer srv.Close()

	_, err := exchangeNpmToken(context.Background(), "id-tok", "widget", srv.URL)
	if err == nil {
		t.Fatal("want error on 403")
	}
	if !strings.Contains(err.Error(), "trusted publishing") || !strings.Contains(err.Error(), "403") {
		t.Errorf("error missing registry message or status: %v", err)
	}
}

func TestOidcPublishToken_EndToEnd(t *testing.T) {
	// Registry server serves both the id-token request (GitHub path) and the
	// exchange. We point ACTIONS_ID_TOKEN_REQUEST_URL and packageSource at it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/idtoken"):
			_, _ = w.Write([]byte(`{"value":"minted-id-token"}`))
		case strings.Contains(r.URL.Path, "/oidc/token/exchange/"):
			if got := r.Header.Get("Authorization"); got != "Bearer minted-id-token" {
				t.Errorf("exchange Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"token":"final-publish-token"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/idtoken")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
	t.Setenv("NPM_ID_TOKEN", "")

	got, err := oidcPublishToken(context.Background(), "widget", srv.URL)
	if err != nil {
		t.Fatalf("oidcPublishToken: %v", err)
	}
	if got != "final-publish-token" {
		t.Errorf("token = %q, want final-publish-token", got)
	}
}
