package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHasOIDCContext(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://example/token")
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
		t.Setenv("NPM_ID_TOKEN", "")
		if !HasOIDCContext() {
			t.Error("want true for GitHub Actions context")
		}
	})
	t.Run("gitlab", func(t *testing.T) {
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
		t.Setenv("NPM_ID_TOKEN", "idtok")
		if !HasOIDCContext() {
			t.Error("want true for GitLab context")
		}
	})
	t.Run("none", func(t *testing.T) {
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
		t.Setenv("NPM_ID_TOKEN", "")
		if HasOIDCContext() {
			t.Error("want false with no CI context")
		}
		// url present but token missing is not a usable context
		t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://example/token")
		if HasOIDCContext() {
			t.Error("want false when request token is missing")
		}
	})
}

func TestFetchIDToken_GitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer reqtok" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("audience"); got != NpmAudience {
			t.Errorf("audience = %q, want %q", got, NpmAudience)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"id-token-abc"}`))
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
	t.Setenv("NPM_ID_TOKEN", "")

	got, err := FetchIDToken(context.Background(), NpmAudience)
	if err != nil {
		t.Fatalf("FetchIDToken: %v", err)
	}
	if got != "id-token-abc" {
		t.Errorf("token = %q, want id-token-abc", got)
	}
}

func TestFetchIDToken_GitLab(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	t.Setenv("NPM_ID_TOKEN", "  gitlab-tok  ")
	got, err := FetchIDToken(context.Background(), NpmAudience)
	if err != nil {
		t.Fatalf("FetchIDToken: %v", err)
	}
	if got != "gitlab-tok" {
		t.Errorf("token = %q, want trimmed gitlab-tok", got)
	}
}

func TestFetchIDToken_NoContext(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	t.Setenv("NPM_ID_TOKEN", "")
	if _, err := FetchIDToken(context.Background(), NpmAudience); err == nil {
		t.Fatal("want error with no CI context")
	}
}
