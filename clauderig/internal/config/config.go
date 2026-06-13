// Package config models clauderig's configuration — the remote, the per-machine
// path maps (the single source of truth pathmap reads), the sync roots and their
// per-OS locations, and retention. Machine maps live here so a synced session
// translates to whatever layout each machine uses.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/core/pathmap"
)

const schemaVersion = 1

// Machine is one computer's path identity: its OS, home directory, and any extra
// known-folder tokens (e.g. a custom $DROPBOX) that paths may be expressed in.
type Machine struct {
	Name   string            `json:"name"`
	OS     string            `json:"os"` // pathmap OS token: macos/windows/linux
	Home   string            `json:"home"`
	Tokens map[string]string `json:"tokens,omitempty"`
}

// Folders builds the known-folder table for resolving/portablizing on this
// machine: HOME plus any custom tokens.
func (m Machine) Folders() pathmap.MapFolders {
	f := pathmap.MapFolders{"HOME": m.Home}
	for k, v := range m.Tokens {
		f[k] = v
	}
	return f
}

// Resolver returns a pathmap resolver that expands portable templates into this
// machine's native paths.
func (m Machine) Resolver() *pathmap.Resolver {
	return pathmap.NewResolver(m.Folders(), m.OS, nil)
}

// Retention controls the project-history window and when the history orphan
// branch is squashed (size-based: squash when the branch's git footprint exceeds
// Factor × the retained working-tree size, but never below FloorBytes).
type Retention struct {
	HistoryDays  int     `json:"historyDays"`
	SquashFactor float64 `json:"squashFactor"`
	FloorBytes   int64   `json:"floorBytes"`
}

// Root is a sync root: an id, whether it's enabled, and its per-OS location as a
// cascade of portable templates (resolved against a machine's home/OS).
type Root struct {
	ID       string          `json:"id"`
	Enabled  bool            `json:"enabled"`
	Location pathmap.Cascade `json:"location"`
}

// Worktree configures `clauderig worktree`. It's a pointer on Config so an
// unconfigured install serializes nothing here and falls through to the
// defaults (auto-open in a new VS Code window).
type Worktree struct {
	// AutoOpen controls whether `worktree new` opens the new sibling checkout in
	// a separate window for review. Defaults to true; set false to just print the
	// path (the per-run equivalent is `worktree new --no-open`). It's a pointer so
	// an explicit false is distinguishable from "unset" (which means the default).
	AutoOpen *bool `json:"autoOpen,omitempty"`
	// OpenCmd is the command used to open a worktree for review. The worktree path
	// is appended as the final argument, so the value is the program plus any
	// flags — e.g. "code -n" (the default), "cursor -n", "code-insiders -n",
	// "subl -n", "idea". It is whitespace-split into argv and run directly (no
	// shell), so quotes/pipes/globs are not interpreted.
	OpenCmd string `json:"openCmd,omitempty"`
}

// Config is the clauderig configuration document.
type Config struct {
	Schema    int                `json:"schema"`
	Remote    string             `json:"remote,omitempty"`
	Machines  map[string]Machine `json:"machines"`
	Roots     []Root             `json:"roots"`
	Retention Retention          `json:"retention"`
	// AlwaysPrune makes `restore` prune stale config files (skills/commands/
	// agents/plans deleted upstream) by default, as if --prune were passed.
	// `restore --prune=false` overrides it for a single run.
	AlwaysPrune bool `json:"alwaysPrune,omitempty"`
	// AutoRestore makes the SessionStart hook (`clauderig pull`) also restore on a
	// FRESH machine (no projects yet) — auto-wiring a new computer. It deliberately
	// never restores over an established machine (would churn/clobber).
	AutoRestore bool `json:"autoRestore,omitempty"`
	// Worktree configures `clauderig worktree` (auto-open and what to open). Nil
	// when unconfigured; read it through the WorktreeAutoOpen/WorktreeOpenCmd
	// accessors, which apply the defaults.
	Worktree *Worktree `json:"worktree,omitempty"`
}

// DefaultWorktreeOpenCmd is the command `worktree` uses to open a checkout for
// review when none is configured: a new VS Code window (`code -n <path>`).
var DefaultWorktreeOpenCmd = []string{"code", "-n"}

// WorktreeAutoOpen reports whether `worktree new` should open a review window.
// Defaults to true when unset.
func (c *Config) WorktreeAutoOpen() bool {
	if c.Worktree != nil && c.Worktree.AutoOpen != nil {
		return *c.Worktree.AutoOpen
	}
	return true
}

// WorktreeOpenCmd returns the configured open command as argv (program + flags),
// or DefaultWorktreeOpenCmd when unset/blank. The worktree path is appended by
// the caller.
func (c *Config) WorktreeOpenCmd() []string {
	if c.Worktree != nil {
		if fields := strings.Fields(c.Worktree.OpenCmd); len(fields) > 0 {
			return fields
		}
	}
	return append([]string{}, DefaultWorktreeOpenCmd...)
}

// Default returns a config with the standard roots and retention, no machines or
// remote yet (init fills those).
func Default() *Config {
	return &Config{
		Schema:    schemaVersion,
		Machines:  map[string]Machine{},
		Roots:     DefaultRoots(),
		Retention: Retention{HistoryDays: 30, SquashFactor: 2.0, FloorBytes: 500 << 20},
	}
}

// DefaultRoots is the CLI + Desktop roots with their per-OS locations. The CLI
// root is identical everywhere ($HOME/.claude); the Desktop root differs per OS.
func DefaultRoots() []Root {
	return []Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: "$HOME/.claude"}},
		{ID: "desktop", Enabled: true, Location: pathmap.Cascade{PerOS: map[string]string{
			pathmap.OSMacOS:   "$HOME/Library/Application Support/Claude",
			pathmap.OSWindows: "$HOME/AppData/Roaming/Claude",
			pathmap.OSLinux:   "$HOME/.config/Claude",
		}}},
	}
}

// RootLocation resolves root rootID's absolute location on machine m.
func (c *Config) RootLocation(rootID string, m Machine) (string, pathmap.Status) {
	for _, r := range c.Roots {
		if r.ID == rootID {
			return resolveRoot(r, m)
		}
	}
	return "", pathmap.StatusInvalid
}

func resolveRoot(r Root, m Machine) (string, pathmap.Status) {
	res := m.Resolver().Resolve(r.Location.RawFor(m.OS))
	return res.Path, res.Status
}

// Detect builds a Machine for the host this binary runs on.
func Detect(name string) Machine {
	home, _ := os.UserHomeDir()
	return Machine{Name: name, OS: OSToken(), Home: home}
}

// OSToken maps runtime.GOOS to the pathmap OS token.
func OSToken() string {
	switch runtime.GOOS {
	case "windows":
		return pathmap.OSWindows
	case "darwin":
		return pathmap.OSMacOS
	default:
		return pathmap.OSLinux
	}
}

// Save writes the config as indented JSON to dir/config.json.
func Save(c *Config, dir string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), append(b, '\n'), 0o644)
}

// Load reads dir/config.json.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Dir is clauderig's config directory (~/.clauderig), where config.json lives.
func Dir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".clauderig"), nil
}

// StagingDir is the local staging repo (~/.clauderig/repo) that sync pushes from.
func StagingDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "repo"), nil
}

// LoadOrDefault loads the saved config, falling back to Default ONLY when no
// config file exists. A present-but-corrupt config.json (parse error, permission
// issue) is surfaced rather than silently replaced with defaults.
func LoadOrDefault() (*Config, error) {
	d, err := Dir()
	if err != nil {
		return nil, err
	}
	c, err := Load(d)
	if err == nil {
		return c, nil
	}
	if os.IsNotExist(err) {
		return Default(), nil
	}
	return nil, fmt.Errorf("load config (%s): %w", filepath.Join(d, "config.json"), err)
}
