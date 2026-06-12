// Package ghrepo wraps the GitHub CLI (gh) for clauderig's private-repo
// requirement. A sync remote is accepted ONLY when gh can confirm it is a private
// GitHub repo — creation is always --private, and an existing URL is verified via
// `gh repo view`. No gh, non-GitHub, or public repos are refused, no exceptions:
// the data being synced is your Claude Code state and must never land in a public
// or unverifiable repo.
package ghrepo

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Available reports whether the gh CLI is installed.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// ParseSlug extracts owner/repo from a GitHub remote URL (ssh, https, or ssh://).
// ok is false for any non-github.com remote — clauderig can't verify privacy of
// those, so they are rejected.
func ParseSlug(remote string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(remote)
	var path string
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		path = strings.TrimPrefix(s, "git@github.com:")
	case strings.HasPrefix(s, "https://github.com/"):
		path = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		path = strings.TrimPrefix(s, "ssh://git@github.com/")
	default:
		return "", "", false
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsPrivate asks gh whether owner/repo is private.
func IsPrivate(ctx context.Context, slug string) (bool, error) {
	out, err := runGH(ctx, "repo", "view", slug, "--json", "isPrivate", "--jq", ".isPrivate")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// CreatePrivate creates a private repo under the authenticated user and returns
// its HTTPS clone URL. HTTPS works out of the box with gh's git credential helper
// (gh is mandatory anyway), whereas SSH needs separately-configured keys.
func CreatePrivate(ctx context.Context, name string) (httpsURL string, err error) {
	if _, err := runGH(ctx, "repo", "create", name, "--private", "--clone=false"); err != nil {
		return "", err
	}
	url, err := runGH(ctx, "repo", "view", name, "--json", "url", "--jq", `.url + ".git"`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(url), nil
}

// EnsurePrivate is the enforcement gate: remote must be a GitHub repo that gh
// confirms is private. Every failure mode (gh absent, non-GitHub, unverifiable,
// or public) is an error — there is no path to configuring a non-private remote.
func EnsurePrivate(ctx context.Context, remote string) error {
	if !Available() {
		return fmt.Errorf("the GitHub CLI (gh) is required to guarantee the sync repo is private — install gh and try again")
	}
	owner, repo, ok := ParseSlug(remote)
	if !ok {
		return fmt.Errorf("clauderig only supports private GitHub repos; %q is not a github.com URL it can verify", remote)
	}
	priv, err := IsPrivate(ctx, owner+"/"+repo)
	if err != nil {
		return fmt.Errorf("could not verify %s/%s is private via gh (does it exist? are you logged in?): %w", owner, repo, err)
	}
	if !priv {
		return fmt.Errorf("%s/%s is PUBLIC — clauderig requires a private repo, no exceptions", owner, repo)
	}
	return nil
}

func runGH(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
