package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/auth"
)

// oidcPublishToken mints an OIDC id-token from the CI provider and exchanges it
// at the npm registry for a short-lived publish token (npm "trusted
// publishing"). The general id-token fetch lives in core/auth; the
// registry-specific exchange — endpoint shape and audience — is npm's, so it
// lives here in the adapter.
func oidcPublishToken(ctx context.Context, pkgName, packageSource string) (string, error) {
	idToken, err := auth.FetchIDToken(ctx, auth.NpmAudience)
	if err != nil {
		return "", err
	}
	return exchangeNpmToken(ctx, idToken, pkgName, packageSource)
}

// exchangeNpmToken POSTs the id-token to the registry's OIDC exchange endpoint
// and returns the issued publish token. A non-2xx is a configuration error
// (most often: the package has no trusted publisher for this workflow) and is
// surfaced verbatim so the operator can fix it before any package ships.
func exchangeNpmToken(ctx context.Context, idToken, pkgName, packageSource string) (string, error) {
	base := npmRegistryBase(packageSource)
	endpoint := base + "/-/npm/v1/oidc/token/exchange/package/" + url.PathEscape(pkgName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")

	resp, err := oidcHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("npm OIDC token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("npm OIDC token exchange rejected (%s) for %s: %s — "+
			"is this workflow configured as a Trusted Publisher for the package? "+
			"(set NPM_TOKEN to fall back)",
			resp.Status, pkgName, npmExchangeMessage(body))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("npm OIDC token exchange: decode response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("npm OIDC token exchange returned no token for %s", pkgName)
	}
	return out.Token, nil
}

// npmRegistryBase normalizes a packageSource to a registry base URL with no
// trailing slash. A non-URL source (feed name / empty) means the public
// registry.
func npmRegistryBase(packageSource string) string {
	if strings.HasPrefix(packageSource, "http") {
		return strings.TrimSuffix(packageSource, "/")
	}
	return "https://registry.npmjs.org"
}

// npmExchangeMessage pulls the registry's human message out of an error body,
// falling back to the raw (trimmed, bounded) body.
func npmExchangeMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Error != "" {
			return e.Error
		}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

// npmSupportsProvenance reports whether the local npm can attach a provenance
// attestation (npm ≥ 11.5.1). A missing/unparseable npm is treated as "no".
func npmSupportsProvenance(ctx context.Context) bool {
	out, _, err := runCmd(ctx, "", "npm", "--version")
	if err != nil {
		return false
	}
	return npmVersionAtLeast(strings.TrimSpace(out), 11, 5, 1)
}

// npmVersionAtLeast reports whether semver string v is >= maj.min.pat. A
// pre-release/build suffix is ignored; an unparseable version returns false.
func npmVersionAtLeast(v string, maj, min, pat int) bool {
	v = strings.SplitN(v, "-", 2)[0]
	v = strings.SplitN(v, "+", 2)[0]
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	c, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	switch {
	case a != maj:
		return a > maj
	case b != min:
		return b > min
	default:
		return c >= pat
	}
}

// oidcHTTP is the client for the exchange; var so tests can swap it.
var oidcHTTP = &http.Client{Timeout: 30 * time.Second}
