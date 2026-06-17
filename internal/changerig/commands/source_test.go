package commands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/ecosystem"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeF(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initGoRepo creates a git repo with a single root Go module, an initial
// release tag, and returns the resolved repo path.
func initGoRepo(t *testing.T, module, tag string) string {
	t.Helper()
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "test@example.com")
	gitCmd(t, dir, "config", "user.name", "Test")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	writeF(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26\n")
	writeF(t, filepath.Join(dir, "main.go"), "package main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "chore: initial")
	gitCmd(t, dir, "tag", tag)
	return dir
}

// TestLoadChangesetsCommitMode: in commits mode, LoadChangesets synthesizes
// changesets from the conventional commits since the package's release tag, and
// ignores both pre-tag commits and non-conventional ones.
func TestLoadChangesetsCommitMode(t *testing.T) {
	dir := initGoRepo(t, "example.com/a", "v1.0.0")

	// A feature commit after the tag, plus a non-conventional one that must be
	// ignored.
	writeF(t, filepath.Join(dir, "feature.go"), "package main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "feat: add a feature")
	writeF(t, filepath.Join(dir, "notes.txt"), "wip\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "wip nonconventional")

	cfg, err := config.Parse([]byte(`{"versioning":{"source":"commits"}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: cfg, Registry: ecosystem.Default()}

	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sets, fromCommits, err := ws.LoadChangesets(context.Background(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if !fromCommits {
		t.Error("fromCommits should be true in commits mode")
	}
	if len(sets) != 1 {
		t.Fatalf("got %d changesets, want 1 (only the feat commit): %+v", len(sets), sets)
	}
	cs := sets[0]
	if cs.Type != "feat" {
		t.Errorf("type = %q, want feat", cs.Type)
	}
	if names := cs.ChangedNames(); len(names) != 1 || names[0] != "example.com/a" {
		t.Errorf("names = %v, want [example.com/a]", names)
	}
	if cs.Summary != "add a feature" {
		t.Errorf("summary = %q, want %q", cs.Summary, "add a feature")
	}
}

// TestLoadChangesetsInitialReleaseCollapse: a package with no prior release tag
// and initialRelease.collapse enabled gets one "Initial release" changeset at
// the configured bump instead of one changeset per historical commit.
func TestLoadChangesetsInitialReleaseCollapse(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "init", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "test@example.com")
	gitCmd(t, dir, "config", "user.name", "Test")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	writeF(t, filepath.Join(dir, "go.mod"), "module example.com/a\n\ngo 1.26\n")
	writeF(t, filepath.Join(dir, "main.go"), "package main\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-m", "feat: first")
	// Several more conventional commits — all history, no release tag yet.
	for _, msg := range []string{"feat: second", "fix: third", "docs: fourth"} {
		writeF(t, filepath.Join(dir, "f_"+msg[len(msg)-2:]+".go"), "package main\n")
		gitCmd(t, dir, "add", "-A")
		gitCmd(t, dir, "commit", "-m", msg)
	}

	cfg, err := config.Parse([]byte(`{"versioning":{"source":"commits","initialRelease":{"collapse":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: cfg, Registry: ecosystem.Default()}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sets, _, err := ws.LoadChangesets(context.Background(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 {
		t.Fatalf("got %d changesets, want 1 (collapsed initial release): %+v", len(sets), sets)
	}
	cs := sets[0]
	if cs.Summary != "Initial release" {
		t.Errorf("summary = %q, want %q", cs.Summary, "Initial release")
	}
	if len(cs.Releases) != 1 || cs.Releases[0].Name != "example.com/a" {
		t.Fatalf("releases = %+v, want one for example.com/a", cs.Releases)
	}
	if cs.Releases[0].Bump != changeset.BumpMinor {
		t.Errorf("bump = %v, want minor (default)", cs.Releases[0].Bump)
	}
}

// TestLoadChangesetsChangesetModeUnaffected: the default still reads on-disk
// changeset files and never touches the commit log.
func TestLoadChangesetsChangesetModeUnaffected(t *testing.T) {
	dir := initGoRepo(t, "example.com/a", "v1.0.0")
	csDir := filepath.Join(dir, ".changeset")
	writeF(t, filepath.Join(csDir, "brave-otters-dance.md"), "---\n\"example.com/a\": minor\n---\n\nhand-written\n")

	ws := &Workspace{Root: dir, ChangesetDir: csDir, Config: config.Default(), Registry: ecosystem.Default()}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sets, fromCommits, err := ws.LoadChangesets(context.Background(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if fromCommits {
		t.Error("fromCommits should be false in changeset mode")
	}
	if len(sets) != 1 || sets[0].ID != "brave-otters-dance" {
		t.Fatalf("got %+v, want the one on-disk changeset", sets)
	}
}
