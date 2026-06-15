package dotnet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestExchangeNugetKey_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer id-tok" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		if in["username"] != "alice" || in["tokenType"] != "ApiKey" {
			t.Errorf("body = %v", in)
		}
		_, _ = w.Write([]byte(`{"apiKey":"nuget-key-123"}`))
	}))
	defer srv.Close()

	got, err := exchangeNugetKey(context.Background(), srv.URL, "alice", "id-tok")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got != "nuget-key-123" {
		t.Errorf("key = %q", got)
	}
}

func TestExchangeNugetKey_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"username does not match policy creator"}`))
	}))
	defer srv.Close()

	_, err := exchangeNugetKey(context.Background(), srv.URL, "bob", "id-tok")
	if err == nil {
		t.Fatal("want error on 403")
	}
	if !strings.Contains(err.Error(), "username does not match policy creator") {
		t.Errorf("error missing service message: %v", err)
	}
}

func TestNugetOIDCKey_RequiresUser(t *testing.T) {
	if _, err := nugetOIDCKey(context.Background(), "  "); err == nil {
		t.Fatal("want error when username is empty")
	} else if !strings.Contains(err.Error(), "dotnet.user") {
		t.Errorf("error should mention dotnet.user: %v", err)
	}
}

func TestNugetAPIKey_Precedence(t *testing.T) {
	// secret-ref wins and is reported.
	k, note, err := nugetAPIKey(context.Background(), plugin.PublishRequest{
		Auth: &plugin.AuthCredential{Token: "ref-key", Method: "secret-ref"},
	})
	if err != nil || k != "ref-key" || !strings.Contains(note, "secret reference") {
		t.Errorf("secret-ref: key=%q note=%q err=%v", k, note, err)
	}

	// no auth, no OIDC → falls back to env.
	t.Setenv("NUGET_API_KEY", "env-key")
	k, _, err = nugetAPIKey(context.Background(), plugin.PublishRequest{})
	if err != nil || k != "env-key" {
		t.Errorf("env fallback: key=%q err=%v", k, err)
	}
}

func TestRedact(t *testing.T) {
	if got := redact("pushing with --api-key secret123 now", "secret123"); strings.Contains(got, "secret123") {
		t.Errorf("secret not redacted: %q", got)
	}
	if got := redact("no secret here", ""); got != "no secret here" {
		t.Errorf("empty secret changed text: %q", got)
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
	notes := strings.Join(on.Notes, "\n")
	if !strings.Contains(notes, "Trusted Publishing") || !strings.Contains(notes, "dotnet.user") {
		t.Errorf("OIDC on: notes missing setup/username guidance: %v", on.Notes)
	}

	off, err := a.ReleaseInit(context.Background(), plugin.ReleaseInitRequest{OIDC: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Tokens) != 1 || off.Tokens[0].EnvVar != "NUGET_API_KEY" {
		t.Errorf("OIDC off: want NUGET_API_KEY, got %+v", off.Tokens)
	}
}
