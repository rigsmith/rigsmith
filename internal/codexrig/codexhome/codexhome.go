// Package codexhome is the vendor seam: everything codexrig knows about where
// the Codex CLI keeps its state, and nothing else. It is the one place a Codex
// upgrade that moves a file has to be edited.
//
// Two rules hold it together. First, every path is derived from a home directory
// passed in, never from the environment at the point of use — an account's
// isolated home and the machine's default home are the same shape, and a helper
// that silently read CODEX_HOME would make the isolated case unreachable.
// Second, nothing here decides policy: whether a file may be synced is the
// allowlist's business, and whether it may be copied is the account's.
package codexhome

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvHome is the environment variable that relocates Codex's home. Setting it is
// how codexrig isolates one login's state from another's: the same mechanism
// CLAUDE_CONFIG_DIR provides for Claude Code.
const EnvHome = "CODEX_HOME"

// DirName is the home directory's name under $HOME.
const DirName = ".codex"

// Default is Codex's home for this machine when nothing relocates it:
// $HOME/.codex. It deliberately ignores CODEX_HOME — a machine's layout is not
// whatever one shell happened to export, and reading the variable here is what
// would let `codexrig account add` file one login's credential under another's
// identity.
func Default() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, DirName), nil
}

// Env reports the home CODEX_HOME names, and whether it was set at all. Callers
// use it to REFUSE, not to resolve: an operation that mutates machine-wide state
// while the variable points somewhere else is acting on a different Codex than
// the one the user is looking at.
func Env() (string, bool) {
	v, ok := os.LookupEnv(EnvHome)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// Config is the user-level config file inside home: config.toml.
func Config(home string) string { return filepath.Join(home, "config.toml") }

// ProfileConfig is the file a named profile's overlay lives in. Codex resolves
// `--profile NAME` to NAME.config.toml beside the main config (the shape since
// 0.134.0; older releases used nested [profiles.NAME] tables inside config.toml,
// which Layers still reports so a restore does not silently drop them).
func ProfileConfig(home, name string) string {
	return filepath.Join(home, name+".config.toml")
}

// ProfileName recovers the profile name from a config file's base name, or ""
// when the name is not a profile overlay. "config.toml" itself is not a profile.
func ProfileName(base string) string {
	base = filepath.Base(base)
	if base == "config.toml" {
		return ""
	}
	name, ok := strings.CutSuffix(base, ".config.toml")
	if !ok || name == "" {
		return ""
	}
	return name
}

// Auth is the credential file inside home. NEVER synced, never copied between
// accounts: Codex writes and refreshes it, and codexrig's whole safety claim is
// that a credential stays on the machine that earned it.
func Auth(home string) string { return filepath.Join(home, "auth.json") }

// Instructions is the user-level instruction file inside home: AGENTS.md, the
// counterpart to Claude Code's CLAUDE.md.
func Instructions(home string) string { return filepath.Join(home, "AGENTS.md") }

// Sessions is the rollout tree inside home. Codex shards it by date:
// sessions/YYYY/MM/DD/rollout-<stamp>-<uuid>.jsonl.
func Sessions(home string) string { return filepath.Join(home, "sessions") }

// History is Codex's cross-session prompt history file.
func History(home string) string { return filepath.Join(home, "history.jsonl") }

// Hooks is the standalone hook-definition file, when Codex is configured that
// way rather than through a [hooks] table in config.toml.
func Hooks(home string) string { return filepath.Join(home, "hooks.json") }

// Prompts is the user's saved-prompt directory ("custom prompts" / slash
// commands), the nearest counterpart to Claude Code's commands/.
func Prompts(home string) string { return filepath.Join(home, "prompts") }

// Skills is the user-level skills directory inside home.
func Skills(home string) string { return filepath.Join(home, "skills") }

// Log is Codex's log directory — machine-local diagnostics, never synced.
func Log(home string) string { return filepath.Join(home, "log") }

// Exists reports whether home looks like a Codex home at all: the directory is
// there. Deliberately weak — a home that exists but holds nothing yet is the
// normal state of a freshly installed Codex, and demanding config.toml would
// make `codexrig init` refuse the very machine it is meant to set up.
func Exists(home string) bool {
	st, err := os.Stat(home)
	return err == nil && st.IsDir()
}

// SameDir reports whether two paths name the same directory, resolving symlinks
// where they exist. Used to tell "CODEX_HOME points at the default home"
// (harmless) from "CODEX_HOME points somewhere else" (a refusal).
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}
