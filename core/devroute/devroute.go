// Package devroute is the shared home for the dev launchers' "active route" —
// the worktree a repo's `<tool>-dev` launchers build from by default.
//
// The route is a per-repo pin: a tiny state file holding one absolute worktree
// path. `rig worktree use/unset` writes it; the generated `<tool>-dev`
// wrappers read it (with a plain `cat`, so `-dev` stays a build-and-exec fast
// path with no clauderig subprocess). Both sides agree on the file location
// because they compute it the same way here — dev-install bakes RouteFile's
// result into each wrapper at install time, and clauderig resolves it live.
//
// The pin lives in the XDG state dir (~/.local/state/rigsmith/dev-routes/<slug>,
// or $XDG_STATE_HOME/rigsmith/... when set), keyed by the repo's path. It's a
// rigsmith-wide bit of machine-local dev state — read by every tool's `-dev`
// launcher — so it belongs under a shared rigsmith namespace, alongside the
// launchers in ~/.local/bin, not in clauderig's (synced) config dir. State home
// is the FHS-correct place for data that persists between runs but isn't config.
package devroute

import (
	"os"
	"path/filepath"
	"strings"
)

// dirName is the directory under the rigsmith state dir that holds the pins.
const dirName = "dev-routes"

// slug turns a repo path into a single filename-safe segment by replacing every
// byte outside [A-Za-z0-9] with '_'. It mirrors the dev wrapper's existing
// `tr -c 'A-Za-z0-9' _`, so a path keys to the same file whether the slug is
// formed here or (historically) in the shell.
func slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range []byte(s) {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteByte(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// stateDir is rigsmith's XDG state home: $XDG_STATE_HOME/rigsmith when set,
// else ~/.local/state/rigsmith. The launchers that read these pins install to
// the sibling ~/.local/bin, so this keeps all rigsmith dev artifacts together.
func stateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "rigsmith"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "state", "rigsmith"), nil
}

// RouteFile returns the pin file path for repoRoot:
// <state>/dev-routes/<slug>. repoRoot should be absolute and clean (callers pass
// the git toplevel) so the same repo always keys to the same file.
func RouteFile(repoRoot string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dirName, slug(filepath.Clean(repoRoot))), nil
}

// Read returns the pinned worktree path for repoRoot, or "" when nothing is
// pinned (the file is absent or blank). A missing file is not an error: no pin
// is the normal unconfigured state.
func Read(repoRoot string) (string, error) {
	file, err := RouteFile(repoRoot)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Write pins target as repoRoot's active route, creating the dev-routes dir as
// needed. The file holds the single absolute path, newline-terminated.
func Write(repoRoot, target string) error {
	file, err := RouteFile(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(target+"\n"), 0o644)
}

// Unset clears repoRoot's pin. A missing pin is treated as success — unsetting
// when nothing is pinned is a no-op, not an error.
func Unset(repoRoot string) error {
	file, err := RouteFile(repoRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
