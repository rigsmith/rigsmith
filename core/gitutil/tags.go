// Package gitutil wraps the small set of git operations the engine needs:
// reading the latest version tag for a module and (later) creating tags. It
// shells out to the `git` binary — the same delegation model used elsewhere —
// and degrades gracefully when git or a repo is absent.
package gitutil

import (
	"context"
	"os/exec"
	"strings"

	"github.com/rigsmith/rigsmith/core/semver"
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

// PackageTag returns the canonical git tag for a package release: Go modules use
// the module-path convention (dir/vX.Y.Z); every other ecosystem uses
// name@version. This is the single source of truth shared by the tag/publish
// steps and the forge (GitHub release) step, so a release attaches to the tag
// that was actually pushed instead of creating a divergent one.
func PackageTag(eco, dirRel, name, version string) string {
	if eco == "go" {
		return ModuleTag(dirRel, version)
	}
	return name + "@" + version
}

// RenderTag returns the git tag for a package release, honoring a configured
// tag template. An empty template falls back to PackageTag (the canonical
// per-ecosystem name). A non-empty template is expanded with the ${version} and
// ${name} placeholders — e.g. "v${version}" yields "v1.2.3" for the single-app
// vX.Y.Z convention. The same template is applied wherever a release tag is
// built (creation, the forge release, and the ${tag} variable) so they agree.
func RenderTag(template, eco, dirRel, name, version string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return PackageTag(eco, dirRel, name, version)
	}
	r := strings.NewReplacer("${version}", version, "${name}", name)
	return r.Replace(template)
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

// RemoteTagExists reports whether tag is present on the given remote. Used to
// distinguish "fully published" from "tagged locally but the push failed", so a
// re-run can recover by pushing the existing tag rather than skipping it.
func RemoteTagExists(ctx context.Context, repoRoot, remote, tag string) bool {
	out, err := runGit(ctx, repoRoot, "ls-remote", "--tags", remote, "refs/tags/"+tag)
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

// GitHubRepoSlug returns the "owner/repo" of the repo's GitHub origin remote, or
// "" when there is no remote, it isn't a github.com URL, or git is absent. Used
// to link changelog contributors to their GitHub pages without requiring the
// changelog-github generator to be configured.
func GitHubRepoSlug(ctx context.Context, repoRoot string) string {
	remote := DefaultRemote(ctx, repoRoot)
	if remote == "" {
		return ""
	}
	out, err := runGit(ctx, repoRoot, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return parseGitHubSlug(strings.TrimSpace(out))
}

// parseGitHubSlug extracts "owner/repo" from a github.com remote URL in either
// SSH (git@github.com:owner/repo.git) or HTTPS (https://github.com/owner/repo)
// form. Returns "" for non-github or unparseable URLs.
func parseGitHubSlug(url string) string {
	url = strings.TrimSuffix(url, ".git")
	i := strings.Index(url, "github.com")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(url[i+len("github.com"):], ":/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
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
