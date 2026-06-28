// Ported from net-changesets Shared/GitServiceTests.cs, but against real
// temporary git repositories instead of a mocked process executor (the Go
// package shells out directly, so the repo IS the seam).
package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a git repo with one initial commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// macOS: /var/folders is a symlink to /private/var; resolve so paths from
	// `git rev-parse --show-toplevel` compare equal.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "README.md"), "hello\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "initial")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestModuleTag(t *testing.T) {
	cases := []struct{ dirRel, version, want string }{
		{"", "1.2.3", "v1.2.3"},
		{".", "1.2.3", "v1.2.3"},
		{"core", "0.3.0", "core/v0.3.0"},
		{"core/", "0.3.0", "core/v0.3.0"},
		{"./core", "0.3.0", "core/v0.3.0"},
		{"a\\b", "1.0.0", "a/b/v1.0.0"},   // windows separators normalize
		{"core", "v0.3.0", "core/v0.3.0"}, // leading v not doubled
	}
	for _, c := range cases {
		if got := ModuleTag(c.dirRel, c.version); got != c.want {
			t.Errorf("ModuleTag(%q, %q) = %q, want %q", c.dirRel, c.version, got, c.want)
		}
	}
}

func TestLatestModuleVersion(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	for _, tag := range []string{"v1.0.0", "v1.2.0", "v1.3.0-rc.1", "core/v0.3.0", "core/v0.2.0", "not-a-version"} {
		git(t, dir, "tag", tag)
	}

	// Root module: stable 1.2.0 vs prerelease 1.3.0-rc.1 — prereleases are
	// compared by semver precedence, and 1.3.0-rc.1 > 1.2.0.
	if v, ok := LatestModuleVersion(ctx, dir, ""); !ok || v != "1.3.0-rc.1" {
		t.Errorf("root LatestModuleVersion = %q, %v; want 1.3.0-rc.1, true", v, ok)
	}
	// Submodule tags are namespaced by directory.
	if v, ok := LatestModuleVersion(ctx, dir, "core"); !ok || v != "0.3.0" {
		t.Errorf("core LatestModuleVersion = %q, %v; want 0.3.0, true", v, ok)
	}
	// No matching tags → ok=false (caller falls back).
	if _, ok := LatestModuleVersion(ctx, dir, "other"); ok {
		t.Error("module without tags should report ok=false")
	}
	// Not a repo at all → ok=false, no error surfaced.
	if _, ok := LatestModuleVersion(ctx, t.TempDir(), ""); ok {
		t.Error("non-repo should report ok=false")
	}
}

func TestTagExistsAndCreateTag(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)

	if TagExists(ctx, dir, "pkg@1.0.0") {
		t.Error("tag should not exist yet")
	}
	if err := CreateTag(ctx, dir, "pkg@1.0.0", "pkg 1.0.0"); err != nil {
		t.Fatal(err)
	}
	if !TagExists(ctx, dir, "pkg@1.0.0") {
		t.Error("tag should exist after CreateTag")
	}
	// Annotated, with the given message.
	if msg := git(t, dir, "tag", "-l", "--format=%(contents:subject)", "pkg@1.0.0"); msg != "pkg 1.0.0" {
		t.Errorf("tag message = %q, want %q", msg, "pkg 1.0.0")
	}
	// Re-creating an existing tag is a no-op, not an error.
	if err := CreateTag(ctx, dir, "pkg@1.0.0", "different"); err != nil {
		t.Errorf("CreateTag on existing tag = %v, want nil", err)
	}
	// Empty message defaults to the tag name.
	if err := CreateTag(ctx, dir, "bare", ""); err != nil {
		t.Fatal(err)
	}
	if msg := git(t, dir, "tag", "-l", "--format=%(contents:subject)", "bare"); msg != "bare" {
		t.Errorf("default tag message = %q, want %q", msg, "bare")
	}
}

// TestPackageTag pins the per-ecosystem tag convention: Go modules use the
// module-path form (dir/vX.Y.Z, or vX.Y.Z at the root), every other ecosystem
// uses name@version.
func TestPackageTag(t *testing.T) {
	cases := []struct {
		eco, dirRel, name, version, want string
	}{
		{"go", "core", "core", "1.2.0", "core/v1.2.0"},
		{"go", ".", "root", "1.2.0", "v1.2.0"},
		{"node", "", "my-pkg", "1.2.0", "my-pkg@1.2.0"},
	}
	for _, c := range cases {
		if got := PackageTag(c.eco, c.dirRel, c.name, c.version); got != c.want {
			t.Errorf("PackageTag(%q,%q,%q,%q) = %q, want %q",
				c.eco, c.dirRel, c.name, c.version, got, c.want)
		}
	}
}

// TestRenderTag pins that an empty template falls back to the canonical
// PackageTag, while a template expands ${version}/${name} — the single-app
// v-prefix convention being the motivating case.
func TestRenderTag(t *testing.T) {
	cases := []struct {
		template, eco, dirRel, name, version, want string
	}{
		// Empty template => canonical per-ecosystem tag.
		{"", "node", "", "my-pkg", "1.2.0", "my-pkg@1.2.0"},
		{"", "go", "core", "core", "1.2.0", "core/v1.2.0"},
		// Single-app v-prefix.
		{"v${version}", "dotnet", "", "Halyards.Desktop", "1.0.0", "v1.0.0"},
		// ${name} placeholder, and whitespace is trimmed.
		{"  ${name}-${version}  ", "node", "", "web", "2.1.0", "web-2.1.0"},
	}
	for _, c := range cases {
		if got := RenderTag(c.template, c.eco, c.dirRel, c.name, c.version); got != c.want {
			t.Errorf("RenderTag(%q,%q,%q,%q,%q) = %q, want %q",
				c.template, c.eco, c.dirRel, c.name, c.version, got, c.want)
		}
	}
}

// TestRemoteTagExists pins that RemoteTagExists distinguishes a tag that was
// pushed to the remote from one that is absent there — the signal a re-run uses
// to recover a locally-tagged-but-unpushed release.
func TestRemoteTagExists(t *testing.T) {
	ctx := context.Background()

	// Bare "remote" repo plus a working repo with it added as origin.
	remoteDir := t.TempDir()
	git(t, remoteDir, "init", "--bare", "-b", "main")
	work := initRepo(t)
	git(t, work, "remote", "add", "origin", remoteDir)
	git(t, work, "push", "origin", "main")

	// Create a tag locally and push it.
	git(t, work, "tag", "pkg@1.0.0")
	git(t, work, "push", "origin", "pkg@1.0.0")

	if !RemoteTagExists(ctx, work, "origin", "pkg@1.0.0") {
		t.Error("pushed tag should be reported present on the remote")
	}
	if RemoteTagExists(ctx, work, "origin", "pkg@9.9.9") {
		t.Error("absent tag should be reported missing on the remote")
	}
}

func TestShortHead(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	want := git(t, dir, "rev-parse", "--short", "HEAD")
	if got := ShortHead(ctx, dir); got != want {
		t.Errorf("ShortHead = %q, want %q", got, want)
	}
	if got := ShortHead(ctx, t.TempDir()); got != "" {
		t.Errorf("ShortHead outside a repo = %q, want empty", got)
	}
}

func TestDefaultRemote(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	if got := DefaultRemote(ctx, dir); got != "" {
		t.Errorf("repo without remotes: DefaultRemote = %q, want empty", got)
	}
	git(t, dir, "remote", "add", "upstream", "https://example.com/up.git")
	if got := DefaultRemote(ctx, dir); got != "upstream" {
		t.Errorf("DefaultRemote = %q, want upstream", got)
	}
	git(t, dir, "remote", "add", "origin", "https://example.com/origin.git")
	if got := DefaultRemote(ctx, dir); got != "origin" {
		t.Errorf("DefaultRemote = %q, want origin (preferred)", got)
	}
}

func TestChangedFilesSinceDiffsFromMergeBaseFullPaths(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)

	// Branch off main, then advance main so the merge-base is NOT the tip of
	// main — the diff must be from the fork point, not from main's head.
	git(t, dir, "checkout", "-b", "feature")
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "file.txt"), "committed change\n")
	writeFile(t, filepath.Join(dir, ".changeset", "brave-pandas-smile.md"), "---\n---\n\nx\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "feature work")
	git(t, dir, "checkout", "main")
	writeFile(t, filepath.Join(dir, "main-only.txt"), "advanced\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "main moves on")
	git(t, dir, "checkout", "feature")

	// An uncommitted edit to a TRACKED file is part of the diff too (the diff
	// runs against the working tree); untracked files are not — same as the C#
	// and Node implementations.
	writeFile(t, filepath.Join(dir, "README.md"), "hello edited\n")
	writeFile(t, filepath.Join(dir, "untracked.txt"), "never added\n")

	files, err := ChangedFilesSince(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		filepath.Join(dir, "packages", "pkg-a", "file.txt"):       true,
		filepath.Join(dir, ".changeset", "brave-pandas-smile.md"): true,
		filepath.Join(dir, "README.md"):                           true,
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
		if !filepath.IsAbs(f) {
			t.Errorf("path %q is not absolute", f)
		}
	}
	for f := range want {
		if !got[f] {
			t.Errorf("missing changed file %s (got %v)", f, files)
		}
	}
	if got[filepath.Join(dir, "main-only.txt")] {
		t.Error("main-only.txt is past the merge-base and must not appear")
	}
	if got[filepath.Join(dir, "untracked.txt")] {
		t.Error("untracked files are not part of the diff")
	}

	// Running from a subdirectory still yields repo-root-resolved paths
	// (--no-relative).
	sub := filepath.Join(dir, "packages")
	fromSub, err := ChangedFilesSince(ctx, sub, "main")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fromSub {
		if f == filepath.Join(dir, ".changeset", "brave-pandas-smile.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("from subdir: expected repo-root-resolved path, got %v", fromSub)
	}
}

func TestChangedFilesSinceInvalidRef(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	if _, err := ChangedFilesSince(ctx, dir, "no-such-ref"); err == nil {
		t.Error("invalid ref should error")
	}
	if _, err := ChangedFilesSince(ctx, t.TempDir(), "main"); err == nil {
		t.Error("non-repo should error")
	}
}
