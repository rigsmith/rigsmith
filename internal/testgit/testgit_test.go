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

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
}

func repo(t *testing.T) string {
	t.Helper()
	needGit(t)
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

// hold keeps the live hermetic environment across a test that calls configure
// for itself: t.Setenv restores what was there when the test ends.
func hold(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		t.Setenv(k, os.Getenv(k))
	}
}

// The directory's name is predictable and the temp directory is shared on some
// machines, so it could be there already as somebody else's symlink — pointing
// git at their config, and a config can name core.hooksPath.
func TestASymlinkWhereOurDirectoryGoesIsRefused(t *testing.T) {
	hold(t)
	root := t.TempDir()
	target := filepath.Join(root, "theirs")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "rigsmith-hermetic-git")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := configure(root); err == nil {
		t.Error("configure adopted a symlinked directory")
	}
}

// A quote in the path used to produce a config file git refuses outright, which
// would have been reported as a hermetic run that was not one.
func TestAPathGitsParserWouldChokeOnIsStillWritten(t *testing.T) {
	hold(t)
	root := filepath.Join(t.TempDir(), `aw"kward\path`)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Skipf("this filesystem will not hold the name: %v", err)
	}
	if err := configure(root); err != nil {
		t.Fatal(err)
	}
	dir := repo(t)
	got := strings.TrimSpace(git(t, dir, "config", "--get", "core.excludesFile"))
	want := filepath.ToSlash(filepath.Join(root, "rigsmith-hermetic-git", "ignore"))
	if got != want {
		t.Errorf("core.excludesFile = %q, want %q", got, want)
	}
}

// A line break cannot be escaped into a single-line config value at all.
func TestALineBreakInThePathIsRefused(t *testing.T) {
	if _, err := value("/tmp/two\nlines/ignore"); err == nil {
		t.Error("a path with a line break was accepted as a config value")
	}
}

// CI configured a global identity for the tests, and this package takes the
// global config away, so it has to bring one. useConfigOnly is what makes this
// test mean anything off Linux: without it git invents an identity from the
// username and hostname, which is exactly why eight failures on Linux CI had
// passed on every developer machine.
func TestACommitNeedsNoIdentityFromTheMachine(t *testing.T) {
	dir := repo(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.useConfigOnly=true", "commit", "-qm", "one")
}

// init.defaultBranch came from the same place, so a test that runs `git init`
// itself rather than going through gitrepo.Init would land on whatever this
// git's built-in default is.
func TestANewRepoIsOnMain(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", ".")
	if got := strings.TrimSpace(git(t, dir, "branch", "--show-current")); got != "main" {
		t.Errorf("a fresh repo is on %q, want main", got)
	}
}

// `go test ./...` starts one of these binaries per package, all at once, all
// reaching for the same directory. Whoever loses the race to create it used to
// be killed by init for its trouble — so a cold machine's first run would lose
// a scattering of test binaries to a directory that was, by then, perfectly
// fine.
func TestBinariesStartingTogetherAllGetTheDirectory(t *testing.T) {
	hold(t)
	root := t.TempDir()
	const racers = 24
	errs := make(chan error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		go func() {
			<-start
			errs <- configure(root)
		}()
	}
	close(start)
	for i := 0; i < racers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a binary that started alongside the others was turned away: %v", err)
		}
	}
	// And what they left behind is a config, not a torn one: every one of them
	// wrote the same three files over each other while git could have been
	// reading them.
	body, err := os.ReadFile(filepath.Join(root, "rigsmith-hermetic-git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"excludesFile", "attributesFile", "useConfigOnly", "defaultBranch"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the config they left has no %s in it:\n%s", want, body)
		}
	}
}

// The directory is checked for symlinks; the three files inside it have to be
// too. A link whose target happens to hold what we would write is not our file,
// and leaving it there lets whoever made it keep choosing what git reads —
// core.hooksPath among other things.
func TestASymlinkedConfigFileIsReplacedNotAccepted(t *testing.T) {
	hold(t)
	root := t.TempDir()
	dir := filepath.Join(root, "rigsmith-hermetic-git")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Whatever configure would write, planted behind a link.
	theirs := filepath.Join(root, "theirs")
	if err := os.WriteFile(theirs, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "ignore") // configure writes this one empty
	if err := os.Symlink(theirs, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := configure(root); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("a symlink holding the right contents was left in place for git to read through")
	}
}
