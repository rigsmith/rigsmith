// Package config loads rig's optional per-repo `.rig.json`. The file is plain
// JSON (no JSONC); a missing file is not an error — rig is convention-first and
// works with zero configuration, so the loader returns zero-value defaults when
// nothing is found. Only what can't be inferred lives here.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// FileName is the per-repo config file rig looks for.
const FileName = ".rig.json"

// Config is the subset of the .NET/Node `rig` config rig (Go) understands today.
// Fields map to the shared top-level keys; ecosystem-specific blocks are ignored
// for now (see docs/PORTING-PLAN.md).
type Config struct {
	// DefaultProject names the project to act on when several are runnable.
	DefaultProject string `json:"defaultProject,omitempty"`
	// Ecosystem pins the primary ecosystem (dotnet/node/go/cargo) for the dev
	// verbs and `ui`, overriding nearest-manifest resolution. Set this to
	// disambiguate a directory where several ecosystems coexist.
	Ecosystem string `json:"ecosystem,omitempty"`
	// Quiet suppresses the `→ command` echo before running a verb.
	Quiet bool `json:"quiet,omitempty"`
	// Exclude is a list of globs hiding projects from discovery/pickers.
	// TODO(docs/PORTING-PLAN.md): wire into discovery/pickers; loaded, not yet enforced.
	Exclude []string `json:"exclude,omitempty"`
	// Env is extra environment applied to spawned commands.
	// TODO(docs/PORTING-PLAN.md): apply to runCommand/runShell; loaded, not yet enforced.
	Env map[string]string `json:"env,omitempty"`
	// Commands are custom verbs (npm-scripts style): name → shell string.
	Commands map[string]string `json:"commands,omitempty"`
	// Kill configures the `kill` verb's pattern-based sweep.
	Kill Kill `json:"kill,omitempty"`

	// Path is the resolved location the config was loaded from, "" if none.
	Path string `json:"-"`
}

// Kill configures `rig kill`'s default (no-arg, no-port) sweep. When Match is
// set it overrides project-name inference outright — the patterns are matched
// against the full command line (pkill -f), so they can also target strays that
// aren't rig projects (a hung test host, an IDE-launched instance).
type Kill struct {
	// Match are command-line substrings to kill on a bare `rig kill`.
	Match []string `json:"match,omitempty"`
}

// Load reads the nearest .rig.json found at root (typically detect.Root(cwd)).
// A missing file yields a zero-value Config with no error. A present-but-invalid
// file returns the parse error so the user can fix it.
func Load(root string) (Config, error) {
	path := filepath.Join(root, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	c.Path = path
	return c, nil
}
