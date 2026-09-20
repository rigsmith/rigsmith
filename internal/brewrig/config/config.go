// Package config models brewrig's configuration: the private remote holding the
// machine inventories, this machine's name, and the few knobs that change what
// sync and apply do.
//
// It is deliberately much smaller than clauderig's and codexrig's. Those tools
// sync files and so need roots, allowlists, retention and redaction; brewrig
// syncs a package list, which has no secrets, no per-OS path rewriting and no
// unbounded history. Where a key does exist here it means what it means in the
// siblings — `remote` and `machines` especially — because someone running two
// rigs should not have to learn two vocabularies.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/jsonc"
)

const schemaVersion = 1

// SchemaURL is stamped onto written config.json files, matching the other
// rigsmith configs.
const SchemaURL = "https://rigsmith.dev/schemas/brewrig.json"

var writer = confkit.Writer{SchemaURL: SchemaURL}

// Config is ~/.brewrig/config.json.
type Config struct {
	Schema int `json:"schema"`
	// Remote is the private git repo holding machines/*.json.
	Remote string `json:"remote"`
	// Machine is this machine's name — the stem of the file it publishes, and
	// the name the other machine sees in a prompt. Stored rather than derived
	// at each run so that renaming the Mac in System Settings does not orphan
	// its inventory and make it look like a brand-new machine.
	Machine string `json:"machine"`
	// Branch is the branch published to; empty means "main".
	Branch string `json:"branch,omitempty"`
	// GreedyCasks extends `update` to casks that update themselves. Off by
	// default: upgrading one can restart a running app.
	GreedyCasks bool `json:"greedyCasks,omitempty"`
	// AutoApply lets `sync` install what is missing in the same run, instead
	// of only publishing and reporting. Removals are never automatic
	// regardless of this.
	AutoApply bool `json:"autoApply,omitempty"`
}

// Default is the config a fresh install starts from.
func Default() *Config {
	return &Config{Schema: schemaVersion, Machine: DefaultMachineName(), Branch: "main"}
}

// BranchOrDefault is Branch with the default applied.
func (c *Config) BranchOrDefault() string {
	if c.Branch == "" {
		return "main"
	}
	return c.Branch
}

// OSToken is the pathmap-style OS token for this machine, matching the value
// the sibling rigs write.
func OSToken() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

// DefaultMachineName proposes a name for this machine: the hostname with the
// noise macOS adds stripped. "John's MacBook Pro.local" is a poor filename and
// a worse git path, so it becomes "Johns-MacBook-Pro".
func DefaultMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "machine"
	}
	h = strings.TrimSuffix(h, ".local")
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	return SanitizeMachineName(h)
}

// SanitizeMachineName reduces a name to something safe as a filename on every
// OS and as a git path: letters, digits, dot, dash, underscore.
func SanitizeMachineName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.':
			// Consecutive dots are collapsed so ".." can never appear.
			// Stripping the separator already makes traversal impossible, but
			// an invariant that is checkable beats one that has to be argued
			// from the absence of a slash.
			if !strings.HasSuffix(b.String(), ".") {
				b.WriteRune('.')
			}
		case r == ' ':
			b.WriteRune('-')
		}
		// Anything else — the curly apostrophe macOS puts in "John's" above
		// all — is dropped rather than transliterated.
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "machine"
	}
	return out
}

// DirEnv overrides the config directory. It exists so a test — or a second,
// throwaway setup — can point brewrig somewhere else without moving $HOME.
//
// Moving $HOME is the obvious trick and it does not work here: Homebrew's own
// output changes when HOME does. On the machine this was built against,
// `brew info --json=v2 --installed` under a fresh HOME silently stopped
// reporting the three formulae that came from third-party taps, which would
// have made brewrig publish an inventory quietly missing them.
const DirEnv = "BREWRIG_HOME"

// Dir is brewrig's config directory (~/.brewrig), where config.json lives.
func Dir() (string, error) {
	if d := os.Getenv(DirEnv); d != "" {
		return d, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".brewrig"), nil
}

// StagingDir is the local clone (~/.brewrig/repo) that sync pushes from.
func StagingDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "repo"), nil
}

// Path is the config file path.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// ErrNotConfigured is returned when brewrig has never been set up here.
var ErrNotConfigured = fmt.Errorf("brewrig is not set up on this machine — run `brewrig init`")

// Load reads config.json. A missing file is ErrNotConfigured; a present but
// unreadable one is surfaced rather than silently replaced with defaults,
// because the fallback would publish under the wrong machine name.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := jsonc.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	if c.Schema > schemaVersion {
		return nil, fmt.Errorf("%s is schema %d, this brewrig understands %d — upgrade brewrig", p, c.Schema, schemaVersion)
	}
	if c.Machine == "" {
		return nil, fmt.Errorf("%s has no machine name — run `brewrig init`", p)
	}
	return &c, nil
}

// Save writes config.json as schema-stamped JSONC, preserving comments the user
// added, like the other rigsmith configs.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	c.Schema = schemaVersion
	b, err := writer.Document("brewrig config — JSONC: comments and trailing commas are allowed.", c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
