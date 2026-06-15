package cargo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/auth"
)

// cratesOIDCToken mints an OIDC id-token from the CI provider and exchanges it
// at crates.io for a short-lived (≈30 min) publish token (RFC 3691 "trusted
// publishing"). The general id-token fetch lives in core/auth; the
// registry-specific exchange is crates.io's, so it lives here.
//
// It returns the token plus a revoke func: crates.io tokens are revocable, and
// the official auth action revokes in its post step, so we DELETE the token
// after publishing rather than leave it valid for its full lifetime.
func cratesOIDCToken(ctx context.Context, packageSource string) (token string, revoke func(), err error) {
	base := cratesRegistryBase(packageSource)
	idToken, err := auth.FetchIDToken(ctx, cratesAudience(base))
	if err != nil {
		return "", nil, err
	}

	token, err = exchangeCratesToken(ctx, base, idToken)
	if err != nil {
		return "", nil, err
	}
	revoke = func() { revokeCratesToken(ctx, base, token) }
	return token, revoke, nil
}

// exchangeCratesToken POSTs the id-token to crates.io's trusted-publishing token
// endpoint and returns the issued publish token. A non-2xx is a configuration
// error (most often: no trusted publisher configured for this workflow) and is
// surfaced so the operator can fix it before any crate ships.
func exchangeCratesToken(ctx context.Context, base, idToken string) (string, error) {
	endpoint := base + "/api/v1/trusted_publishing/tokens"
	body, _ := json.Marshal(map[string]string{"jwt": idToken})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cratesUserAgent)

	resp, err := oidcHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("crates.io OIDC token exchange: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("crates.io OIDC token exchange rejected (%s): %s — "+
			"is this workflow configured as a Trusted Publisher for the crate? "+
			"(set CARGO_REGISTRY_TOKEN to fall back)",
			resp.Status, cratesMessage(respBody))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("crates.io OIDC token exchange: decode response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("crates.io OIDC token exchange returned no token")
	}
	return out.Token, nil
}

// revokeCratesToken best-effort revokes a trusted-publishing token (DELETE the
// token endpoint, authenticated by the token itself). Errors are ignored — the
// token expires on its own; revocation just shortens the window.
func revokeCratesToken(ctx context.Context, base, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/v1/trusted_publishing/tokens", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("User-Agent", cratesUserAgent)
	if resp, err := oidcHTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

// cratesRegistryBase normalizes a packageSource to the registry API base URL
// (no trailing slash). The crates.io aliases ("", "crates.io", "crates") mean
// the public registry; a URL source is used verbatim.
func cratesRegistryBase(packageSource string) string {
	if strings.HasPrefix(packageSource, "http") {
		return strings.TrimSuffix(packageSource, "/")
	}
	return "https://crates.io"
}

// cratesAudience is the OIDC audience for a registry base: the host (the URL
// with its scheme stripped), e.g. "crates.io".
func cratesAudience(base string) string {
	base = strings.TrimPrefix(base, "https://")
	base = strings.TrimPrefix(base, "http://")
	return strings.TrimSuffix(base, "/")
}

// cratesMessage pulls a human error message out of a crates.io error body
// ({"errors":[{"detail":"…"}]}), falling back to the bounded raw body.
func cratesMessage(body []byte) string {
	var e struct {
		Errors []struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err == nil && len(e.Errors) > 0 && e.Errors[0].Detail != "" {
		return e.Errors[0].Detail
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

const cratesUserAgent = "shiprig (https://github.com/rigsmith/rigsmith)"

// oidcHTTP is the client for the exchange; var so tests can swap it.
var oidcHTTP = &http.Client{Timeout: 30 * time.Second}
