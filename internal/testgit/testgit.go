// Package testgit cuts the git a test binary runs off from the machine's own
// git setup, so a repo a test makes is the test's alone.
//
// Import it for its side effect from any test package whose tests can reach
// code that runs git:
//
//	import _ "github.com/rigsmith/rigsmith/internal/testgit"
//
// The ambient setup is not merely noise. Two of its parts bite this module in
// particular:
//
// A tool that subscribes to git's trace2 stream — git-ai does, through a global
// trace2.eventtarget — is told about every command a test runs, and answers by
// writing into the repo it was told about: blobs under .git/ai, loose objects, a
// refs/notes ref. Those writes land asynchronously, AFTER the command being
// watched has returned, so a test measuring .git is measuring a directory a
// second writer is still filling, and a test whose repo lives in t.TempDir gets
// new files in .git/objects after its body ends — which is all it takes for
// RemoveAll to fail with "directory not empty".
//
// Git also reads $XDG_CONFIG_HOME/git/ignore whatever the config files say, and
// one line in it is enough to make a test lie: a machine ignoring
// **/.claude/settings.local.json will not commit that file in a test that is
// there to prove agent settings get backed up, while CI, which ignores nothing,
// proves the opposite.
//
// Clearing the config files takes the rest of the ambient surface with it —
// hooksPath, init.templatedir, gc and pack tuning — leaving these tests
// exercising git's behaviour rather than this machine's.
package testgit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	// Never in a real binary: this mutates the process environment, and a
	// rigsmith that ignored the user's own git config would be a bug, not a
	// convenience.
	if !testing.Testing() {
		return
	}
	detach()
	if err := configure(os.TempDir()); err != nil {
		// Not a warning. Carrying on here would run the tests against this
		// machine's git configuration while looking exactly like a run that did
		// not — which is the failure this package exists to remove, and the one
		// that costs a day to find because CI, having nothing ambient to leak,
		// says the opposite.
		fmt.Fprintf(os.Stderr, "testgit: cannot make git hermetic: %v\n", err)
		os.Exit(1)
	}
}

// detach closes the channels that reach git through the environment rather than
// through a config file, before anything is written.
func detach() {
	// Trace2 targets are read from the environment as well as from config, so
	// this — not the config files below — is what silences a subscriber, and it
	// is set first so it holds even if the rest cannot be written.
	for _, k := range []string{"GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF"} {
		_ = os.Setenv(k, "0")
	}
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// The system attributes file is not named by any config key, so the config
	// below cannot reach it.
	_ = os.Setenv("GIT_ATTR_NOSYSTEM", "1")
	// Two channels outrank every config file: the ones git itself uses to pass
	// `-c` down to the commands it spawns. They arrive here whenever the test
	// run was started by git — from a hook, or a `git rebase --exec` — and they
	// would quietly put back the excludesFile or hooksPath the config below is
	// removing. Verified: with GIT_CONFIG_PARAMETERS set, git reports the
	// ambient core.excludesFile and not ours.
	//
	// The rest name which repository git acts on. Inherited from a hook they
	// point at the repo the hook is running in, so a test that means to work in
	// its own temp repo would be reading, and writing, this one.
	for _, k := range []string{
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_NAMESPACE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR",
	} {
		_ = os.Unsetenv(k)
	}

}

// configure points git at a configuration of our own, written under root.
//
// One directory, reused by every test binary and rewritten each time, rather
// than a fresh temp dir per binary: an init has nowhere to hang a cleanup, and
// forty of them per `go test ./...` would be forty leaks.
func configure(root string) error {
	dir := filepath.Join(root, "rigsmith-hermetic-git")
	if err := claim(dir); err != nil {
		return err
	}
	ignore := filepath.Join(dir, "ignore")
	attrs := filepath.Join(dir, "attributes")
	excludes, err := value(ignore)
	if err != nil {
		return err
	}
	attributes, err := value(attrs)
	if err != nil {
		return err
	}
	// The identity and the default branch are not hygiene, they are things the
	// tests need and were getting from the machine: CI configured all three
	// globally, and taking the global config away took them too. Linux git then
	// refuses to invent an author — "Author identity unknown", eight tests, the
	// first CI run that got far enough to say so — while macOS git guesses one
	// from the username and hostname and carries on, which is why nothing local
	// noticed.
	//
	// The identity matches the one gitrepo.Init falls back to, so a repo made
	// through Init and a repo made by a test running `git init` itself are
	// authored the same.
	//
	// useConfigOnly is what stops the guessing, and it is the reason this is
	// here rather than only in CI's setup: without it macOS and Windows quietly
	// author commits as whoever is logged in, on whatever the machine calls
	// itself, and a missing identity is a Linux-only failure discovered in CI.
	// With it, every platform fails the same way in the same place.
	cfg := "[core]\n\texcludesFile = " + excludes + "\n\tattributesFile = " + attributes + "\n" +
		"[user]\n\tname = rigsmith\n\temail = rigsmith@localhost\n\tuseConfigOnly = true\n" +
		"[init]\n\tdefaultBranch = main\n"
	path := filepath.Join(dir, "config")
	for _, f := range []struct{ path, body string }{
		{path, cfg}, {ignore, ""}, {attrs, ""},
	} {
		if err := write(f.path, f.body); err != nil {
			return err
		}
	}
	if err := os.Setenv("GIT_CONFIG_GLOBAL", path); err != nil {
		return err
	}
	return os.Setenv("GIT_CONFIG_SYSTEM", path)
}

// claim makes the directory ours or says why it is not.
//
// The name is predictable, and the temp directory is shared on some machines,
// so it could already be there as somebody else's symlink — which would point
// git at their config, and a config can name core.hooksPath. Checked rather
// than made unique: a unique directory per test binary is forty of them per
// `go test ./...`, since an init has nowhere to hang a cleanup. A directory
// that is theirs and private simply fails the writes that follow, which now
// stops the run rather than being shrugged off.
func claim(dir string) error {
	for attempt := 0; ; attempt++ {
		if err := claimOnce(dir, attempt); err != errRaced {
			return err
		}
	}
}

// errRaced says another test binary created the directory between this one's
// look and its own attempt. `go test ./...` starts forty of these at once, so
// that is ordinary rather than a failure: go round and check what the winner
// made. Killing the binary over it, which is what returning the error did,
// turns a cold machine's first run into a scattering of dead test binaries.
var errRaced = errors.New("another binary created the directory first")

func claimOnce(dir string, attempt int) error {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		err := os.Mkdir(dir, 0o700)
		switch {
		case err == nil:
			return nil
		case !errors.Is(err, fs.ErrExist):
			return err
		case attempt > 0:
			return fmt.Errorf("%s keeps being created and removed underneath us", dir)
		}
		return errRaced
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink, not a directory we made", dir)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	case runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0:
		// Tightened rather than refused, because the repair and the check are
		// the same question: chmod succeeds only for the owner, so a directory
		// that closes here was always ours — and one that is not ours fails,
		// which is the answer we wanted anyway. Earlier runs of this package
		// made the directory 0755, so without this every machine that has run
		// the tests once would need a human to delete it.
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// value renders path as a git config value.
//
// Quoted, with the two characters git's parser reads inside quotes escaped: a
// temp directory carrying either would otherwise produce a file git refuses
// outright — verified, `fatal: bad config line 2` — or, worse, one that parses
// into something else. A newline cannot be escaped into a single-line value at
// all, so it is refused here.
func value(path string) (string, error) {
	if strings.ContainsAny(path, "\n\r") {
		return "", fmt.Errorf("temp directory path contains a line break: %q", path)
	}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + esc.Replace(filepath.ToSlash(path)) + `"`, nil
}

// settled reports whether path is already a file of ours saying what we would
// say — the test that lets forty binaries leave each other's work alone.
//
// Lstat, not ReadFile: content equality through a symlink says the link's
// TARGET matches, and returning early there would leave the link in place, so
// whoever made it keeps choosing what git reads. The directory is checked for
// that; these three files have to be too. A link is not settled, so the rename
// below replaces the link itself.
func settled(path, body string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	current, err := os.ReadFile(path)
	return err == nil && string(current) == body
}

// write replaces path atomically. `go test ./...` starts these binaries at once,
// and a git reading the config file while another binary rewrote it in place
// would see half of one.
//
// Two things follow from forty binaries wanting the same three files. A file
// that already says what we would say is left alone, so only the first run on a
// machine writes anything at all. And the rename is retried, because on Windows
// replacing a file another process holds open fails — one git reading the
// config at the wrong moment is enough — where on Unix it simply succeeds. If
// the file ends up right anyway, whoever put it there did our work.
func write(path, body string) error {
	if settled(path, body) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	for attempt := 0; ; attempt++ {
		err := os.Rename(tmp, path)
		if err == nil {
			return nil
		}
		if settled(path, body) {
			os.Remove(tmp)
			return nil
		}
		if attempt >= 4 {
			os.Remove(tmp)
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
