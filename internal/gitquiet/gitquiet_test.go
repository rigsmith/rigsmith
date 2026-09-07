package gitquiet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The settings are worth nothing unless git actually reads them, and the
// GIT_CONFIG_* protocol is easy to get subtly wrong — a gap in the indices and
// git stops reading at the gap, silently.
func TestGitReadsTheSettings(t *testing.T) {
	dir := t.TempDir()
	for _, c := range [][]string{{"init", "-q", "-b", "main"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, c...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
	for _, want := range [][2]string{{"maintenance.auto", "false"}, {"gc.auto", "0"}} {
		out, err := exec.Command("git", "-C", dir, "config", "--get", want[0]).Output()
		if err != nil {
			t.Fatalf("git did not read %s: %v", want[0], err)
		}
		if got := strings.TrimSpace(string(out)); got != want[1] {
			t.Errorf("%s = %q, want %q", want[0], got, want[1])
		}
	}
}

// And the whole point: a commit must not leave a detached process behind to
// race the cleanup that is about to delete the repository.
func TestCommitSpawnsNoDetachedMaintenance(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")

	cmd := exec.Command("git", "-C", dir, "commit", "-qm", "one")
	cmd.Env = append(os.Environ(), "GIT_TRACE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "maintenance run --auto") {
		t.Errorf("commit still spawned background maintenance:\n%s", out)
	}
}

// Entries already in the environment survive: git reads GIT_CONFIG_KEY_<n> for
// n below GIT_CONFIG_COUNT, so appending has to continue the numbering rather
// than start it again.
func TestApplyKeepsExistingEntries(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "someone")
	Apply()

	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "3" {
		t.Fatalf("count = %q, want 3", got)
	}
	if got := os.Getenv("GIT_CONFIG_KEY_0"); got != "user.name" {
		t.Errorf("the existing entry was overwritten: key_0 = %q", got)
	}
	if got := os.Getenv("GIT_CONFIG_KEY_1"); got != "maintenance.auto" {
		t.Errorf("key_1 = %q, want maintenance.auto", got)
	}
}
