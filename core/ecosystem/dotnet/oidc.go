package dotnet

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

// Defaults for NuGet.org trusted publishing. Custom feeds (Azure Artifacts,
// GitHub Packages) do not support OIDC today, so OIDC is scoped to nuget.org.
const (
	nugetAudience        = "https://www.nuget.org"
	nugetTokenServiceURL = "https://www.nuget.org/api/v2/token"
)

// nugetOIDCKey mints an OIDC id-token from the CI provider and exchanges it at
// NuGet.org for a short-lived (≈1h) API key (NuGet "trusted publishing"). The
// general id-token fetch lives in core/auth; the registry-specific exchange is
// NuGet's, so it lives here.
//
// NuGet's exchange is keyed to a username — the NuGet account that created the
// trusted-publishing policy — so user is required.
func nugetOIDCKey(ctx context.Context, user string) (string, error) {
	if strings.TrimSpace(user) == "" {
		return "", fmt.Errorf("NuGet OIDC needs a username: set `dotnet.user` to the NuGet account " +
			"that created the trusted-publishing policy")
	}
	idToken, err := auth.FetchIDToken(ctx, nugetAudience)
	if err != nil {
		return "", err
	}
	return exchangeNugetKey(ctx, nugetTokenServiceURL, user, idToken)
}

// exchangeNugetKey POSTs the id-token (+ username) to NuGet's token service and
// returns the issued API key. A non-2xx is a configuration error (most often: a
// username/policy mismatch) surfaced so the operator can fix it before pushing.
func exchangeNugetKey(ctx context.Context, tokenServiceURL, user, idToken string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": user, "tokenType": "ApiKey"})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenServiceURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+idToken)

	resp, err := oidcHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("NuGet OIDC token exchange: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("NuGet OIDC token exchange rejected (%s): %s — "+
			"is this workflow a configured Trusted Publisher, and is `dotnet.user` the "+
			"policy creator's username? (set NUGET_API_KEY to fall back)",
			resp.Status, nugetMessage(respBody))
	}

	var out struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("NuGet OIDC token exchange: decode response: %w", err)
	}
	if out.APIKey == "" {
		return "", fmt.Errorf("NuGet OIDC token exchange returned no apiKey")
	}
	return out.APIKey, nil
}

// nugetMessage pulls a human error message out of a NuGet token-service error
// body ({"error":"…"}), falling back to the bounded raw body.
func nugetMessage(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return e.Error
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

// oidcHTTP is the client for the exchange; var so tests can swap it.
var oidcHTTP = &http.Client{Timeout: 30 * time.Second}
