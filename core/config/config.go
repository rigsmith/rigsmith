// Package config models the shared .changeset/config.json schema. It mirrors the
// @changesets config so a polyglot repo keeps a single file both this tool and
// the JS @changesets tool understand. Ecosystem-specific knobs are nested under
// per-ecosystem keys (e.g. "dotnet", "node", "go") so foreign tools ignore them.
//
// Ported from net-changesets' Shared/ChangesetConfig.cs + DotnetConfig.cs, with
// the .NET-specific `dotnet` block generalized into a per-ecosystem map.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateInternalDependencies controls how far dependents of a changed package
// are bumped.
type UpdateInternalDependencies string

const (
	UpdatePatch UpdateInternalDependencies = "patch"
	UpdateMinor UpdateInternalDependencies = "minor"
)

// VersionStrategy controls how a bump is written when a version is shared (e.g.
// a Directory.Build.props or a workspace-root version).
type VersionStrategy string

const (
	// Lockstep bumps the shared value, moving every inheritor together.
	Lockstep VersionStrategy = "lockstep"
	// Independent writes an inline version per package.
	Independent VersionStrategy = "independent"
)

// Snapshot holds snapshot-release settings (shared @changesets key).
type Snapshot struct {
	UseCalculatedVersion bool   `json:"useCalculatedVersion,omitempty"`
	PrereleaseTemplate   string `json:"prereleaseTemplate,omitempty"`
}

// Config is the parsed .changeset/config.json.
//
// Changelog is kept as json.RawMessage because @changesets allows three shapes:
// false, a string name, or a [name, options] tuple. The changelog package
// interprets it. Format is likewise raw (false | "native" | "prettier" | ...).
type Config struct {
	BaseBranch                 string                     `json:"baseBranch,omitempty"`
	Access                     string                     `json:"access,omitempty"`
	Ignore                     []string                   `json:"ignore,omitempty"`
	Fixed                      [][]string                 `json:"fixed,omitempty"`
	Linked                     [][]string                 `json:"linked,omitempty"`
	UpdateInternalDependencies UpdateInternalDependencies `json:"updateInternalDependencies,omitempty"`
	Snapshot                   Snapshot                   `json:"snapshot,omitempty"`

	// VersionStrategy controls how a shared version (e.g. a Directory.Build.props
	// <Version>) is written: "lockstep" (default, also "") moves every inheritor
	// together; "independent" writes an inline version per package, so each
	// versions on its own changesets. Overridable per run with `version --independent`.
	VersionStrategy VersionStrategy `json:"versionStrategy,omitempty"`

	// Format and Changelog are raw because their JSON shape is polymorphic.
	Format    json.RawMessage `json:"format,omitempty"`
	Changelog json.RawMessage `json:"changelog,omitempty"`

	// ChangelogGroups maps conventional-commit types to a changelog section and
	// the version bump they imply. Configurable; empty falls back to the built-in
	// conventional defaults (see DefaultChangelogGroups).
	ChangelogGroups []ChangelogGroup `json:"changelogGroups,omitempty"`

	// Paths optionally narrows package discovery to these repo-relative roots
	// (globs allowed). Empty means scan the whole repo (minus the usual ignores
	// and .gitignored files).
	Paths []string `json:"paths,omitempty"`

	// Ecosystems holds per-ecosystem config blocks (dotnet/node/go/...), kept raw
	// so each ecosystem adapter decodes its own settings. This generalizes the
	// C# tool's single `dotnet` block.
	Ecosystems map[string]json.RawMessage `json:"-"`

	// raw preserves every top-level key so ecosystem blocks and unknown keys
	// survive a load/inspect cycle.
	raw map[string]json.RawMessage
}

// Default returns a Config with the same defaults as the C# tool.
func Default() *Config {
	return &Config{
		BaseBranch:                 "main",
		Access:                     "restricted",
		Ignore:                     []string{},
		Fixed:                      [][]string{},
		Linked:                     [][]string{},
		UpdateInternalDependencies: UpdatePatch,
		// PrereleaseTemplate left empty by default: the empty-template path joins the
		// non-empty parts ({tag}-{datetime}, or just {datetime} with no tag), which
		// avoids a leading dash when no tag is given. Matches @changesets.
		Snapshot:   Snapshot{},
		Ecosystems: map[string]json.RawMessage{},
		raw:        map[string]json.RawMessage{},
	}
}

// ChangelogGroup maps a conventional-commit type to a changelog section heading
// and the version bump it implies.
type ChangelogGroup struct {
	Type    string `json:"type"`    // "feat", "fix", "perf", ...
	Section string `json:"section"` // "🚀 Enhancements"
	Bump    string `json:"bump"`    // "major" | "minor" | "patch"
}

// DefaultChangelogGroups is the built-in conventional-commit grouping, styled
// after changelogen. A breaking change (a `!` on the type) always maps to major
// and the "Breaking Changes" section regardless of the base type.
var DefaultChangelogGroups = []ChangelogGroup{
	{Type: "feat", Section: "🚀 Enhancements", Bump: "minor"},
	{Type: "fix", Section: "🩹 Fixes", Bump: "patch"},
	{Type: "perf", Section: "🔥 Performance", Bump: "patch"},
	{Type: "refactor", Section: "💅 Refactors", Bump: "patch"},
	{Type: "docs", Section: "📖 Documentation", Bump: "patch"},
	{Type: "build", Section: "📦 Build", Bump: "patch"},
	{Type: "test", Section: "✅ Tests", Bump: "patch"},
	{Type: "chore", Section: "🏡 Chore", Bump: "patch"},
}

// BreakingGroup is the section/bump used for a breaking change (type suffixed `!`).
var BreakingGroup = ChangelogGroup{Type: "breaking", Section: "💥 Breaking Changes", Bump: "major"}

// Groups returns the configured changelog groups, or the conventional defaults.
func (c *Config) Groups() []ChangelogGroup {
	if len(c.ChangelogGroups) > 0 {
		return c.ChangelogGroups
	}
	return DefaultChangelogGroups
}

// Known top-level keys that are NOT per-ecosystem blocks.
var sharedKeys = map[string]bool{
	"$schema": true, "baseBranch": true, "access": true, "ignore": true,
	"fixed": true, "linked": true, "updateInternalDependencies": true,
	"snapshot": true, "format": true, "changelog": true, "commit": true,
	"privatePackages": true, "changelogGroups": true, "paths": true,
}

// Parse decodes config bytes, applying defaults and bucketing unknown
// object-valued keys as ecosystem blocks.
func Parse(data []byte) (*Config, error) {
	cfg := Default()

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	// Re-decode into the raw map to capture ecosystem blocks + unknown keys.
	if err := json.Unmarshal(data, &cfg.raw); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for k, v := range cfg.raw {
		if sharedKeys[k] {
			continue
		}
		// Treat any non-shared key as an ecosystem block. Adapters look themselves
		// up by id; foreign keys are simply ignored by everyone.
		cfg.Ecosystems[k] = v
	}
	return cfg, nil
}

// Load reads and parses .changeset/config.json under the given changeset dir.
func Load(changesetDir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(changesetDir, "config.json"))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// ChangelogSpec interprets the polymorphic `changelog` config value and returns
// the generator id: false/null/absent → "default"; a string → that string; a
// [name, options] tuple → name. Options handling is the generator's concern.
func (c *Config) ChangelogSpec() string {
	raw := bytesTrim(c.Changelog)
	if len(raw) == 0 || string(raw) == "false" || string(raw) == "null" {
		return "default"
	}
	// String form.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return "default"
		}
		return s
	}
	// [name, options] tuple.
	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err == nil && len(tuple) > 0 {
		var name string
		if json.Unmarshal(tuple[0], &name) == nil && name != "" {
			return name
		}
	}
	return "default"
}

// FormatCommand returns the custom formatter command when `format` is an
// array of strings — the escape hatch for tools the built-in table doesn't
// know: `"format": ["myfmt", "--write"]` runs that argv with the touched
// changelog paths appended, exactly as written (no package-manager wrapping —
// spell out `["pnpm", "exec", ...]` if that's what you want). ok=false for
// every other shape, including arrays holding non-strings.
func (c *Config) FormatCommand() (argv []string, ok bool) {
	raw := bytesTrim(c.Format)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	if err := json.Unmarshal(raw, &argv); err != nil || len(argv) == 0 || argv[0] == "" {
		return nil, false
	}
	return argv, true
}

// FormatSpec interprets the polymorphic `format` value and returns the
// formatter name: false/null/absent/"" → "" (disabled); a string → itself;
// true → "true" (mirroring the C# converter — it lands on the
// unknown-formatter warning path rather than silently enabling anything).
// The array form is FormatCommand's; it resolves to "" here.
func (c *Config) FormatSpec() string {
	raw := bytesTrim(c.Format)
	switch {
	case len(raw) == 0, string(raw) == "false", string(raw) == "null":
		return ""
	case string(raw) == "true":
		return "true"
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func bytesTrim(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}

// IsIgnored reports whether the package name matches the `ignore` config (by
// exact name or a '*' glob, e.g. "*Bench" / "Acme.*"). Ignored packages are
// never released, tagged, or published — though their manifest dependency
// ranges are still rewritten (a "none" release).
func (c *Config) IsIgnored(name string) bool {
	for _, pat := range c.Ignore {
		if ignoreGlobMatch(pat, name) {
			return true
		}
	}
	return false
}

// ignoreGlobMatch is a minimal glob supporting '*'.
func ignoreGlobMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(name[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false // anchored prefix
		}
		pos += idx + len(part)
	}
	if last := parts[len(parts)-1]; last != "" && !strings.HasSuffix(name, last) {
		return false // anchored suffix
	}
	return true
}

// Ecosystem decodes the named ecosystem block into dst. Returns false if the
// block is absent (dst is left at its defaults).
func (c *Config) Ecosystem(id string, dst any) (bool, error) {
	raw, ok := c.Ecosystems[id]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("config: ecosystem %q: %w", id, err)
	}
	return true, nil
}
