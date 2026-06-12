// Package gitutil wraps the small set of git operations the engine needs:
// reading the latest version tag for a module and (later) creating tags. It
// shells out to the `git` binary — the same delegation model used elsewhere —
// and degrades gracefully when git or a repo is absent.
package gitutil

import (
	"context"
	"os/exec"
	"strings"

	"github.com/rigsmith/core/semver"
)

// LatestModuleVersion returns the highest semver among the git tags for a Go
// module, following Go's tagging convention:
//
//	root module (dirRel "" or ".")  -> tags "vX.Y.Z"
//	submodule at dirRel "core"      -> tags "core/vX.Y.Z"
//
// It returns the version without the leading "v" and ok=true when a matching
// tag exists. ok=false means no tags (or no git) — the caller falls back to its
// secondary source. Prerelease tags are considered and compared by precedence.
func LatestModuleVersion(ctx context.Context, repoRoot, dirRel string) (version string, ok bool) {
	prefix := tagPrefix(dirRel)
	out, err := runGit(ctx, repoRoot, "tag", "--list", prefix+"v*")
	if err != nil {
		return "", false
	}

	var best semver.Version
	found := false
	for _, line := range strings.Split(out, "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" || !strings.HasPrefix(tag, prefix+"v") {
			continue
		}
		raw := strings.TrimPrefix(tag, prefix+"v")
		v, valid := semver.Parse(raw)
		if !valid {
			continue
		}
		if !found || semver.Compare(v, best) > 0 {
			best = v
			found = true
		}
	}
	if !found {
		return "", false
	}
	return best.String(), true
}

// ModuleTag returns the canonical tag name for a module version, e.g.
// "core/v1.2.3" for a submodule or "v1.2.3" for the root module.
func ModuleTag(dirRel, version string) string {
	return tagPrefix(dirRel) + "v" + strings.TrimPrefix(version, "v")
}

func tagPrefix(dirRel string) string {
	dirRel = strings.TrimPrefix(filepathToSlash(dirRel), "./")
	if dirRel == "" || dirRel == "." {
		return ""
	}
	return strings.TrimSuffix(dirRel, "/") + "/"
}

// filepathToSlash converts OS separators to '/' without importing path/filepath
// (tags always use '/').
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// TagExists reports whether a tag already exists in the repo.
func TagExists(ctx context.Context, repoRoot, tag string) bool {
	out, err := runGit(ctx, repoRoot, "tag", "--list", tag)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// CreateTag creates an annotated tag at HEAD with the given message. It is a
// no-op (nil error) if the tag already exists.
func CreateTag(ctx context.Context, repoRoot, tag, message string) error {
	if TagExists(ctx, repoRoot, tag) {
		return nil
	}
	if message == "" {
		message = tag
	}
	_, err := runGit(ctx, repoRoot, "tag", "-a", tag, "-m", message)
	return err
}

// DefaultRemote returns the repo's first configured remote (preferring "origin"),
// or "" when there is none.
func DefaultRemote(ctx context.Context, repoRoot string) string {
	out, err := runGit(ctx, repoRoot, "remote")
	if err != nil {
		return ""
	}
	remotes := strings.Fields(out)
	for _, r := range remotes {
		if r == "origin" {
			return "origin"
		}
	}
	if len(remotes) > 0 {
		return remotes[0]
	}
	return ""
}

// PushTag pushes a single tag to the given remote.
func PushTag(ctx context.Context, repoRoot, remote, tag string) error {
	_, err := runGit(ctx, repoRoot, "push", remote, tag)
	return err
}

// ShortHead returns the abbreviated HEAD commit hash, or "" when unavailable.
func ShortHead(ctx context.Context, repoRoot string) string {
	out, err := runGit(ctx, repoRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// StageAndCommit stages every change under repoRoot (`git add -A`) and commits
// it with message. It returns committed=false (nil error) when there is nothing
// to commit — the working tree is clean after staging. GPG signing is disabled
// for the commit so it never blocks on a passphrase in CI. This mirrors the
// release orchestrator's built-in `commit` step, reused by `version` when the
// `commit` config key is enabled.
func StageAndCommit(ctx context.Context, repoRoot, message string) (committed bool, err error) {
	if _, err := runGit(ctx, repoRoot, "add", "-A"); err != nil {
		return false, err
	}
	out, err := runGit(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	if message == "" {
		message = "Version Packages"
	}
	if _, err := runGit(ctx, repoRoot, "-c", "commit.gpgsign=false", "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// CommitPaths stages exactly the given paths and commits them with message,
// returning committed=false (nil error) when none of them changed. Unlike
// StageAndCommit it does not sweep the whole tree — the right tool when only a
// specific file (e.g. a freshly written changeset) should be committed. GPG
// signing is disabled.
func CommitPaths(ctx context.Context, repoRoot, message string, paths ...string) (committed bool, err error) {
	if len(paths) == 0 {
		return false, nil
	}
	if _, err := runGit(ctx, repoRoot, append([]string{"add", "--"}, paths...)...); err != nil {
		return false, err
	}
	out, err := runGit(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	if message == "" {
		message = "Add changeset"
	}
	if _, err := runGit(ctx, repoRoot, "-c", "commit.gpgsign=false", "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
