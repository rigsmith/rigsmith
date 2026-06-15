package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OIDC audience for npm's trusted-publishing token exchange. The CI provider
// mints an id-token bound to this audience; the registry only accepts an
// exchange whose token carries it.
const NpmAudience = "npm:registry.npmjs.org"

// HasOIDCContext reports whether the process is running in a CI environment that
// can mint an OIDC id-token (GitHub Actions with id-token permission, or GitLab
// with a configured NPM_ID_TOKEN). It is a cheap presence check — it does not
// prove the token will be accepted, only that an exchange is worth attempting.
func HasOIDCContext() bool {
	if os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") != "" {
		return true
	}
	return os.Getenv("NPM_ID_TOKEN") != ""
}

// FetchIDToken obtains a raw OIDC id-token for the given audience from the
// ambient CI provider. This is the general half of trusted publishing; the
// registry-specific exchange of this token for a publish token lives in the
// ecosystem adapter.
//
//   - GitHub Actions: GET $ACTIONS_ID_TOKEN_REQUEST_URL&audience=<aud> with the
//     $ACTIONS_ID_TOKEN_REQUEST_TOKEN bearer; the token is response .value.
//   - GitLab CI: the user pre-mints it into $NPM_ID_TOKEN (audience configured
//     in .gitlab-ci.yml), so we return it directly.
func FetchIDToken(ctx context.Context, audience string) (string, error) {
	if reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"); reqURL != "" {
		return fetchGitHubIDToken(ctx, reqURL, os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"), audience)
	}
	if tok := strings.TrimSpace(os.Getenv("NPM_ID_TOKEN")); tok != "" {
		return tok, nil
	}
	return "", fmt.Errorf("no OIDC id-token available: not in a supported CI context " +
		"(GitHub Actions needs the `id-token: write` permission)")
}

func fetchGitHubIDToken(ctx context.Context, reqURL, reqToken, audience string) (string, error) {
	if reqToken == "" {
		return "", fmt.Errorf("ACTIONS_ID_TOKEN_REQUEST_TOKEN is empty: did you grant `id-token: write` to this job?")
	}
	u, err := url.Parse(reqURL)
	if err != nil {
		return "", fmt.Errorf("parse id-token request url: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+reqToken)
	req.Header.Set("Accept", "application/json")

	resp, err := oidcHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("request GitHub OIDC token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub OIDC token request failed: %s", strings.TrimSpace(string(body)))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode GitHub OIDC token: %w", err)
	}
	if out.Value == "" {
		return "", fmt.Errorf("GitHub OIDC token response had no value")
	}
	return out.Value, nil
}

// oidcHTTP is the client for OIDC requests; var so tests can swap it.
var oidcHTTP = &http.Client{Timeout: 30 * time.Second}
