package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main", ".")
	return dir
}

// The package's whole claim is that nothing outside the repo and its own
// directory gets a say. Read it back from git rather than from the environment:
// the variables being set is not the same as git honouring them.
func TestNoConfigComesFromOutside(t *testing.T) {
	dir := repo(t)
	hermetic := filepath.Join(os.TempDir(), "rigsmith-hermetic-git")
	out := git(t, dir, "config", "--list", "--show-origin")
	var stray []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		origin, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		path := strings.TrimPrefix(origin, "file:")
		switch {
		case path == origin:
			continue // command line or environment, not a file
		case !filepath.IsAbs(path):
			continue // relative to the repo, so the repo's own
		case strings.HasPrefix(path, hermetic):
			continue // one of ours
		}
		stray = append(stray, line)
	}
	if len(stray) > 0 {
		t.Errorf("config reached the test from outside:\n%s", strings.Join(stray, "\n"))
	}
}

// core.excludesFile has a default path of its own, so emptying the config files
// does not stop the machine's global ignore list from applying. This module
// syncs .claude directories, and a developer whose global ignore drops
// .claude/settings.local.json would watch a backup test prove the file is not
// backed up, while CI proved it was.
func TestGlobalIgnoreDoesNotReachTheRepo(t *testing.T) {
	dir := repo(t)
	rel := filepath.Join(".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "status", "--porcelain", "--ignored=matching", "--", rel)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	if strings.HasPrefix(strings.TrimSpace(string(out)), "!!") {
		t.Errorf("a file the test wrote is ignored by something outside the repo: %s", out)
	}
}
