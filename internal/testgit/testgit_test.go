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
	// Compared against the path we handed git rather than one rebuilt from
	// os.TempDir: on Windows git reports origins with forward slashes, and the
	// temp directory can come back in its short form, so a reconstructed path
	// would not match its own config file and the test would call ours foreign.
	ours := filepath.ToSlash(os.Getenv("GIT_CONFIG_GLOBAL"))
	out := git(t, dir, "config", "--list", "--show-origin")
	var stray []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		origin, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		path, isFile := strings.CutPrefix(origin, "file:")
		switch {
		case !isFile:
			// `command line:` is what git calls GIT_CONFIG_PARAMETERS and
			// GIT_CONFIG_COUNT as well as a literal -c, and it outranks every
			// file below it. Nothing here passes -c, so this is ambient.
		case !filepath.IsAbs(path):
			continue // relative to the repo, so the repo's own
		case ours != "" && filepath.ToSlash(path) == ours:
			continue // the config this package wrote
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

// The one thing worse than a test binary that cannot make git hermetic is one
// that carries on anyway: the run then reads this machine's configuration while
// looking exactly like a run that did not. init() exits on this error, which a
// test cannot observe, so what is checked here is that the error reaches it.
func TestSetupFailureIsReported(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "root")
	// A file where the directory would have to go.
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before := os.Getenv("GIT_CONFIG_GLOBAL")
	if err := configure(blocked); err == nil {
		t.Error("configure reported success with nowhere to write")
	}
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != before {
		t.Errorf("a failed setup still moved GIT_CONFIG_GLOBAL: %q → %q", before, got)
	}
}

// Two environment channels outrank every config file, and git itself sets them
// when it spawns a command — so a test run started from a hook or a
// `git rebase --exec` arrives carrying them. The rest name which repository git
// acts on, which from a hook is this one rather than the test's temp repo.
func TestInheritedEnvironmentChannelsAreDropped(t *testing.T) {
	carried := map[string]string{
		"GIT_CONFIG_PARAMETERS":            "'core.excludesFile=/nowhere/ignore'",
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_DIR":                          "/nowhere/.git",
		"GIT_WORK_TREE":                    "/nowhere",
		"GIT_INDEX_FILE":                   "/nowhere/index",
		"GIT_NAMESPACE":                    "nowhere",
		"GIT_OBJECT_DIRECTORY":             "/nowhere/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": "/nowhere/alt",
		"GIT_COMMON_DIR":                   "/nowhere/common",
	}
	for k, v := range carried {
		t.Setenv(k, v)
	}
	detach()
	for k := range carried {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s survived into the test run as %q", k, v)
		}
	}
}
