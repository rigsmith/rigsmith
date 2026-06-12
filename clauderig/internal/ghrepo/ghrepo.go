// Package ghrepo is clauderig's private-repo enforcement gate. A sync remote is
// accepted ONLY when a provider CLI can confirm it is private: GitHub via `gh`,
// GitLab via `glab`. Any host we can't verify (or a public repo) is refused — the
// synced data is your Claude Code state and must never land somewhere public or
// unverifiable. (GitHub/GitLab Enterprise custom hosts are not auto-detected yet.)
package ghrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Available reports whether the gh CLI is installed.
func Available() bool { return have("gh") }

func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

// parseRemote extracts the host and owner/repo path from a git remote URL
// (ssh, https, or ssh://). The slug may contain GitLab subgroups (a/b/c).
func parseRemote(remote string) (host, slug string, ok bool) {
	s := strings.TrimSpace(remote)
	var rest string
	switch {
	case strings.HasPrefix(s, "git@"):
		s = strings.TrimPrefix(s, "git@")
		i := strings.IndexByte(s, ':')
		if i < 0 {
			return "", "", false
		}
		host, rest = s[:i], s[i+1:]
	case strings.HasPrefix(s, "https://"):
		s = strings.TrimPrefix(s, "https://")
		i := strings.IndexByte(s, '/')
		if i < 0 {
			return "", "", false
		}
		host, rest = s[:i], s[i+1:]
	case strings.HasPrefix(s, "ssh://git@"):
		s = strings.TrimPrefix(s, "ssh://git@")
		i := strings.IndexByte(s, '/')
		if i < 0 {
			return "", "", false
		}
		host, rest = s[:i], s[i+1:]
	default:
		return "", "", false
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	segs := strings.Split(rest, "/")
	if len(segs) < 2 {
		return "", "", false
	}
	for _, seg := range segs {
		if seg == "" {
			return "", "", false
		}
	}
	return host, rest, true
}

// ParseSlug extracts owner/repo from a GitHub remote (back-compat helper; GitHub
// repos are exactly owner/repo). ok is false for any non-github.com remote.
func ParseSlug(remote string) (owner, repo string, ok bool) {
	host, slug, ok := parseRemote(remote)
	if !ok || host != "github.com" {
		return "", "", false
	}
	segs := strings.Split(slug, "/")
	if len(segs) != 2 {
		return "", "", false
	}
	return segs[0], segs[1], true
}

// IsPrivate asks gh whether owner/repo is private.
func IsPrivate(ctx context.Context, slug string) (bool, error) {
	out, err := run(ctx, "gh", "repo", "view", slug, "--json", "isPrivate", "--jq", ".isPrivate")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// CreatePrivate creates a private GitHub repo under the authenticated user and
// returns its HTTPS clone URL (works with gh's git credential helper).
func CreatePrivate(ctx context.Context, name string) (httpsURL string, err error) {
	if _, err := run(ctx, "gh", "repo", "create", name, "--private", "--clone=false"); err != nil {
		return "", err
	}
	url, err := run(ctx, "gh", "repo", "view", name, "--json", "url", "--jq", `.url + ".git"`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(url), nil
}

// EnsurePrivate is the enforcement gate: remote must be a GitHub or GitLab repo
// that the matching CLI confirms is private. gh/glab absent, unsupported host, or
// a public/unverifiable repo are all errors — no path to a non-private remote.
func EnsurePrivate(ctx context.Context, remote string) error {
	host, slug, ok := parseRemote(remote)
	if !ok {
		return fmt.Errorf("clauderig can't parse %q as a github.com or gitlab.com repo URL", remote)
	}
	switch host {
	case "github.com":
		if have("gh") {
			return verify(ctx, slug, IsPrivate)
		}
		if tok := firstEnv("GITHUB_TOKEN", "GH_TOKEN"); tok != "" {
			return verify(ctx, slug, func(c context.Context, s string) (bool, error) { return apiPrivateGitHub(c, s, tok) })
		}
		return fmt.Errorf("verifying %s needs the gh CLI or a GITHUB_TOKEN env var", slug)
	case "gitlab.com":
		if have("glab") {
			return verify(ctx, slug, isPrivateGitLab)
		}
		if tok := firstEnv("GITLAB_TOKEN", "GL_TOKEN"); tok != "" {
			return verify(ctx, slug, func(c context.Context, s string) (bool, error) { return apiPrivateGitLab(c, s, tok) })
		}
		return fmt.Errorf("verifying %s needs the glab CLI or a GITLAB_TOKEN env var", slug)
	default:
		return fmt.Errorf("clauderig verifies private repos on github.com and gitlab.com only; %q (%s) is unsupported", remote, host)
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// apiPrivateGitHub confirms a repo is private via the GitHub REST API using a
// token from the environment (no CLI needed). A 404 means not-found-or-no-access
// — which we treat as an error, never as "assume private".
func apiPrivateGitHub(ctx context.Context, slug, token string) (bool, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+slug, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("not found or token lacks access (HTTP 404)")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("github api: %s", resp.Status)
	}
	var r struct {
		Private bool `json:"private"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, err
	}
	return r.Private, nil
}

// apiPrivateGitLab confirms a project's visibility is private via the GitLab API
// using a token from the environment.
func apiPrivateGitLab(ctx context.Context, slug, token string) (bool, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://gitlab.com/api/v4/projects/"+url.PathEscape(slug), nil)
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("not found or token lacks access (HTTP 404)")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("gitlab api: %s", resp.Status)
	}
	var r struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, err
	}
	return r.Visibility == "private", nil
}

func verify(ctx context.Context, slug string, check func(context.Context, string) (bool, error)) error {
	priv, err := check(ctx, slug)
	if err != nil {
		return fmt.Errorf("could not verify %s is private (does it exist? are you logged in?): %w", slug, err)
	}
	if !priv {
		return fmt.Errorf("%s is not private — clauderig requires a private repo, no exceptions", slug)
	}
	return nil
}

// isPrivateGitLab asks glab whether a project's visibility is private.
func isPrivateGitLab(ctx context.Context, slug string) (bool, error) {
	out, err := run(ctx, "glab", "repo", "view", slug, "--output", "json")
	if err != nil {
		return false, err
	}
	// glab emits JSON with a "visibility" field; private == not public/internal.
	return strings.Contains(out, `"visibility": "private"`) ||
		strings.Contains(out, `"visibility":"private"`), nil
}

func run(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
