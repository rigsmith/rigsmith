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

// VersioningSource selects where a version run reads its release intent from.
type VersioningSource string

const (
	// SourceChangesets (default, also "") consumes on-disk changeset files.
	SourceChangesets VersioningSource = "changesets"
	// SourceCommits synthesizes changesets from conventional commits since the
	// last release.
	SourceCommits VersioningSource = "commits"
	// SourceBoth unions on-disk changesets with commit-derived ones.
	SourceBoth VersioningSource = "both"
)

// Versioning configures commit-based versioning (knope-style). The zero value
// is changeset mode, so existing repos are unaffected.
type Versioning struct {
	// Source is "changesets" (default), "commits", or "both".
	Source VersioningSource `json:"source,omitempty"`
	// Scopes optionally maps a conventional-commit scope to a package name, so
	// `feat(core): …` attributes to the package aliased "core" regardless of
	// which files it touched. A commit whose scope is absent from this map falls
	// back to path-based attribution.
	Scopes map[string]string `json:"scopes,omitempty"`
}

// CommitSource returns the normalized versioning source, defaulting an empty
// value to changeset mode.
func (c *Config) CommitSource() VersioningSource {
	switch c.Versioning.Source {
	case SourceCommits:
		return SourceCommits
	case SourceBoth:
		return SourceBoth
	default:
		return SourceChangesets
	}
}

// UsesCommits reports whether the run reads release intent from commits (either
// "commits" or "both").
func (c *Config) UsesCommits() bool {
	s := c.CommitSource()
	return s == SourceCommits || s == SourceBoth
}

// UsesChangesets reports whether the run reads on-disk changeset files (either
// "changesets" or "both").
func (c *Config) UsesChangesets() bool {
	s := c.CommitSource()
	return s == SourceChangesets || s == SourceBoth
}

// Contributors configures the trailing "❤️ Contributors" changelog section.
type Contributors struct {
	// Enabled turns the section on. Off (the zero value) means no section.
	Enabled bool `json:"enabled,omitempty"`
	// ExcludeBots drops authors whose login/name looks like a bot (`*[bot]`).
	// A pointer so an unset value defaults to true; set it to false to keep bots.
	ExcludeBots *bool `json:"excludeBots,omitempty"`
	// Exclude is a list of author logins, names, or emails to omit (glob `*`
	// supported, same matcher as `ignore`). Lets a maintainer keep their own name
	// off every release.
	Exclude []string `json:"exclude,omitempty"`
	// Section overrides the section heading. Empty uses the default
	// "❤️ Contributors", mirroring how `changelogGroups` set their own headings.
	Section string `json:"section,omitempty"`
}

// DefaultContributorsSection is the heading used when none is configured.
const DefaultContributorsSection = "❤️ Contributors"

// SectionHeading returns the configured contributors heading, or the default.
func (c Contributors) SectionHeading() string {
	if s := strings.TrimSpace(c.Section); s != "" {
		return s
	}
	return DefaultContributorsSection
}

// ExcludesBots reports whether bot authors should be dropped (default true).
func (c Contributors) ExcludesBots() bool {
	return c.ExcludeBots == nil || *c.ExcludeBots
}

// IsContributorExcluded reports whether an author should be omitted from the
// Contributors section — by the bot filter (when on) or any `exclude` pattern,
// matched against the login, name, and email (email is matched but never shown).
func (c Contributors) IsContributorExcluded(login, name, email string) bool {
	if c.ExcludesBots() && (looksLikeBot(login) || looksLikeBot(name)) {
		return true
	}
	for _, pat := range c.Exclude {
		for _, field := range []string{login, name, email} {
			if field != "" && ignoreGlobMatch(pat, field) {
				return true
			}
		}
	}
	return false
}

// looksLikeBot matches the conventional GitHub bot suffix, e.g. `renovate[bot]`.
func looksLikeBot(s string) bool {
	return strings.HasSuffix(s, "[bot]")
}

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
	// versions on its own changesets. Overridable per run with `version --independent`,
	// or per ecosystem with a `versionStrategy` in that ecosystem's block.
	VersionStrategy VersionStrategy `json:"versionStrategy,omitempty"`

	// PerPackageStrategy optionally overrides VersionStrategy for individual
	// packages (keyed by package name). The version/status commands populate it
	// from the per-ecosystem `versionStrategy` blocks before planning; nil means
	// "use VersionStrategy for every package". Runtime-only, never serialized.
	PerPackageStrategy map[string]VersionStrategy `json:"-"`

	// Format, Changelog, and Commit are raw because their JSON shape is polymorphic.
	Format    json.RawMessage `json:"format,omitempty"`
	Changelog json.RawMessage `json:"changelog,omitempty"`
	Commit    json.RawMessage `json:"commit,omitempty"`

	// ChangelogGroups maps conventional-commit types to a changelog section and
	// the version bump they imply. Configurable; empty falls back to the built-in
	// conventional defaults (see DefaultChangelogGroups).
	ChangelogGroups []ChangelogGroup `json:"changelogGroups,omitempty"`

	// Paths optionally narrows package discovery to these repo-relative roots
	// (globs allowed). Empty means scan the whole repo (minus the usual ignores
	// and .gitignored files).
	Paths []string `json:"paths,omitempty"`

	// Versioning selects where release intent comes from: on-disk changesets
	// (default), conventional commits, or both. Absent/zero is changeset mode —
	// the historical behavior.
	Versioning Versioning `json:"versioning,omitempty"`

	// Contributors configures the changelogen-style "Contributors" section.
	// Absent/disabled means no section (the historical behavior).
	Contributors Contributors `json:"contributors,omitempty"`

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
	"versioning": true, "contributors": true,
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

// CommitEnabled interprets the polymorphic `commit` value (mirroring
// @changesets): false/null/absent → false; true → true; a [resolver, options]
// tuple → true (the run auto-commits; rigsmith uses its default message, the
// custom-resolver options are not consulted). Any other shape is false.
func (c *Config) CommitEnabled() bool {
	raw := bytesTrim(c.Commit)
	switch {
	case len(raw) == 0, string(raw) == "false", string(raw) == "null":
		return false
	case string(raw) == "true":
		return true
	case raw[0] == '[':
		// [resolver, options] — a configured resolver means "commit".
		var tuple []json.RawMessage
		if err := json.Unmarshal(raw, &tuple); err == nil && len(tuple) > 0 {
			return true
		}
	}
	return false
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

// EcosystemConfig is the generalized per-ecosystem block (keyed by an
// adapter's id: "dotnet"/"node"/"go"/"cargo"), the polyglot replacement for
// net-changesets' single `dotnet` block. Production code reads these via
// EcoConfig: discovery honors SourcePath, publish honors PackageSource, and the
// planner honors VersionStrategy. Unknown keys in a block are ignored.
type EcosystemConfig struct {
	// SourcePath narrows this ecosystem's discovery to a repo-relative root,
	// overriding the top-level `paths` for this ecosystem only (net's
	// `dotnet.sourcePath`).
	SourcePath string `json:"sourcePath,omitempty"`
	// PackageSource overrides the publish feed/registry for this ecosystem
	// (net's `dotnet.packageSource`); empty falls back to the built-in default.
	PackageSource string `json:"packageSource,omitempty"`
	// VersionStrategy overrides the top-level VersionStrategy for this
	// ecosystem's packages (net's `dotnet.versionStrategy`); empty inherits it.
	VersionStrategy VersionStrategy `json:"versionStrategy,omitempty"`
}

// EcoConfig decodes the per-ecosystem block for id into an EcosystemConfig,
// returning the zero value when the block is absent or malformed (config is
// best-effort: a bad block degrades to defaults rather than failing a run).
func (c *Config) EcoConfig(id string) EcosystemConfig {
	var ec EcosystemConfig
	_, _ = c.Ecosystem(id, &ec)
	return ec
}

// EcoStrategy returns the per-ecosystem versionStrategy override for id, or ""
// when the block does not set one (the caller falls back to VersionStrategy).
func (c *Config) EcoStrategy(id string) VersionStrategy {
	return c.EcoConfig(id).VersionStrategy
}

// StrategyByPackage builds the planner's PerPackageStrategy map from the
// per-ecosystem versionStrategy blocks. ecoOf maps each package name to its
// ecosystem id; packages whose ecosystem sets no override are omitted (they
// inherit the top-level VersionStrategy).
func (c *Config) StrategyByPackage(ecoOf map[string]string) map[string]VersionStrategy {
	out := map[string]VersionStrategy{}
	for name, id := range ecoOf {
		if s := c.EcoStrategy(id); s != "" {
			out[name] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
