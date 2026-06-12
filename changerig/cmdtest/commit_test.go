package cmdtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigJSON writes .changeset/config.json with the given body.
func writeConfigJSON(t *testing.T, root, body string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ".changeset", "config.json"), body)
}

// headSubject returns the subject line of the repo's HEAD commit.
func headSubject(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "log", "-1", "--pretty=%s")
}

// TestVersionCommitsWhenConfigured: with `commit: true`, `version` auto-commits
// the bumps + changelog + changeset deletion as "Version Packages", leaving a
// clean tree.
func TestVersionCommitsWhenConfigured(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeConfigJSON(t, dir, `{ "commit": true }`)
	gitInit(t, dir)

	writeChangeset(t, dir, "brave-otters-dance", "pkg-a", "minor", "Add a feature")

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	if got := headSubject(t, dir); got != "Version Packages" {
		t.Errorf("HEAD subject = %q, want %q\n%s", got, "Version Packages", out)
	}
	// The auto-commit captured everything: the tree is clean afterward.
	if status := git(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("working tree not clean after commit:\n%s", status)
	}
	// The bump landed and the changeset was consumed.
	if pj := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(pj, `"1.1.0"`) {
		t.Errorf("pkg-a not bumped to 1.1.0:\n%s", pj)
	}
	if files := changesetFiles(t, dir); len(files) != 0 {
		t.Errorf("changeset not removed: %v", files)
	}
}

// TestVersionDoesNotCommitByDefault: with no `commit` key, `version` leaves the
// commit to the caller — HEAD is unchanged and the working tree is dirty.
func TestVersionDoesNotCommitByDefault(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeConfigJSON(t, dir, `{ "updateInternalDependencies": "patch" }`)
	gitInit(t, dir)

	writeChangeset(t, dir, "brave-otters-dance", "pkg-a", "minor", "Add a feature")

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	if got := headSubject(t, dir); got != "initial" {
		t.Errorf("HEAD subject = %q, want it unchanged (%q)", got, "initial")
	}
	if status := git(t, dir, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		t.Error("expected a dirty tree (version changes uncommitted) without commit config")
	}
}

// TestAddCommitsTheChangeset: with `commit: true`, `add` commits only the new
// changeset file, using its summary as the message.
func TestAddCommitsTheChangeset(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeConfigJSON(t, dir, `{ "commit": true }`)
	gitInit(t, dir)

	code, out := runChangerig(t, dir, "add", "-p", "pkg-a", "-t", "feat", "-m", "shiny new thing")
	assertExitZero(t, code, out)

	if got := headSubject(t, dir); got != "shiny new thing" {
		t.Errorf("HEAD subject = %q, want %q\n%s", got, "shiny new thing", out)
	}
	// Only the changeset was committed; the tree is otherwise clean.
	if status := git(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("working tree not clean after add commit:\n%s", status)
	}
	if files := changesetFiles(t, dir); len(files) != 1 {
		t.Errorf("expected exactly one changeset committed, got %v", files)
	}
}
