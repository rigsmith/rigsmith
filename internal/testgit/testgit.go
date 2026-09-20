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
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func init() {
	// Never in a real binary: this mutates the process environment, and a
	// rigsmith that ignored the user's own git config would be a bug, not a
	// convenience.
	if !testing.Testing() {
		return
	}
	// Trace2 targets are read from the environment as well as from config, so
	// this — not the config files below — is what silences a subscriber, and it
	// is set first so it holds even if the rest cannot be written.
	for _, k := range []string{"GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF"} {
		_ = os.Setenv(k, "0")
	}
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")

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

// configure points git at a configuration of our own, written under root.
//
// One directory, reused by every test binary and rewritten each time, rather
// than a fresh temp dir per binary: an init has nowhere to hang a cleanup, and
// forty of them per `go test ./...` would be forty leaks.
func configure(root string) error {
	dir := filepath.Join(root, "rigsmith-hermetic-git")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ignore := filepath.Join(dir, "ignore")
	attrs := filepath.Join(dir, "attributes")
	// Forward slashes and quotes: a backslash escapes inside a config value, so a
	// Windows temp path written raw would not survive the parser.
	cfg := "[core]\n\texcludesFile = \"" + filepath.ToSlash(ignore) + "\"\n" +
		"\tattributesFile = \"" + filepath.ToSlash(attrs) + "\"\n"
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

// write replaces path atomically. `go test ./...` starts these binaries at once,
// and a git reading the config file while another binary rewrote it in place
// would see half of one.
func write(path, body string) error {
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
