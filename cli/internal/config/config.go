// Package config loads rig's optional `.rig.json`. The file is JSONC (comments
// and trailing commas tolerated); a missing file is not an error — rig is
// convention-first and works with zero configuration, so the loader returns
// zero-value defaults when nothing is found. A malformed file degrades to
// defaults with a warning rather than failing every command. Only what can't
// be inferred lives here.
//
// This is the Go port of the .NET rig's RigConfig: same schema, same
// normalization (the `dotnet` namespace and top-level `envPresets` fold onto
// the canonical fields right after parse), and the same global+repo merge
// semantics (~/.rig.json layered under the repo's .rig.json).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/rigsmith/core/jsonc"
)

// FileName is the config file rig looks for (per-repo and in $HOME).
const FileName = ".rig.json"

// Config is the `.rig.json` model, mirroring the .NET rig's RigConfig.
// Tolerant: unknown properties are ignored (surfaced via UnknownKeys /
// Warnings), key matching is case-insensitive, and JSONC is accepted.
type Config struct {
	// Solution pins the .sln/.slnx the .NET verbs operate on.
	Solution string `json:"solution,omitempty"`
	// DefaultProject names the project to act on when several are runnable.
	DefaultProject string `json:"defaultProject,omitempty"`
	// DotnetFormatter is the resolved `dotnet.formatter` choice (csharpier |
	// dotnet | auto). Internal: set by normalize() from the dotnet block, not a
	// top-level key.
	DotnetFormatter string `json:"-"`
	// Ecosystem pins the primary ecosystem (dotnet/node/go/cargo) for the dev
	// verbs and `ui`, overriding nearest-manifest resolution. Set this to
	// disambiguate a directory where several ecosystems coexist.
	Ecosystem string `json:"ecosystem,omitempty"`
	// Test configures the test verb (pinned project, env presets).
	Test *Test `json:"test,omitempty"`
	// Coverage configures the coverage verb.
	Coverage *Coverage `json:"coverage,omitempty"`
	// Kill configures the `kill` verb's pattern-based sweep.
	Kill Kill `json:"kill,omitempty"`
	// Rebuild configures the rebuild verb (directories to skip).
	Rebuild *Rebuild `json:"rebuild,omitempty"`
	// Publish configures the publish verb's defaults.
	Publish *Publish `json:"publish,omitempty"`
	// Env is extra environment applied to spawned commands.
	Env map[string]string `json:"env,omitempty"`
	// EnvPresets are named env bundles applied by a matching flag (e.g.
	// `rig test --log`). Transient: normalize folds it onto Test.EnvPresets
	// (the legacy spot) so consumers read a single place.
	EnvPresets map[string]map[string]string `json:"envPresets,omitempty"`
	// Dotnet is the `dotnet` namespace for .NET-specific settings in a config
	// shared across the rig tools. Transient: normalize folds it onto the
	// canonical top-level fields right after parse, so it is always nil by the
	// time a verb reads the config.
	Dotnet *Dotnet `json:"dotnet,omitempty"`
	// Commands are custom verbs: name → shell string, argv array, or an
	// object with description/command/os/env/cwd.
	Commands map[string]*Command `json:"commands,omitempty"`
	// Exclude is a list of globs hiding projects from discovery/pickers.
	Exclude []string `json:"exclude,omitempty"`
	// Quiet suppresses the `→ command` echo before running a verb. A pointer
	// so an unset repo value falls through to the global config in Merge.
	Quiet *bool `json:"quiet,omitempty"`
	// Aliases maps verb name → curated short alias, overriding the built-in
	// default and naming custom verbs' aliases (e.g. "coverage": "c").
	Aliases map[string]string `json:"aliases,omitempty"`

	// Tools maps an optional external tool's name to how rig handles it when
	// it's missing: "auto" (default — use if present, offer to install on a
	// TTY), "off" (never use / never ask), or "install" (acquire without
	// asking). See docs/EXTERNAL-TOOLS.md.
	Tools map[string]string `json:"tools,omitempty"`

	// Worktree configures `rig worktree` (auto-open a review window, and what to
	// open). Nil when unconfigured; read it through the WorktreeAutoOpen /
	// WorktreeOpenCmd accessors, which apply the defaults.
	Worktree *Worktree `json:"worktree,omitempty"`

	// Path is the resolved location the config was loaded from, "" if none.
	Path string `json:"-"`
	// Warnings collects non-fatal load problems (malformed file degraded to
	// defaults, unknown keys with did-you-mean suggestions). The loader never
	// prints; the caller decides what to surface.
	Warnings []string `json:"-"`
}

// IsQuiet reports whether quiet mode is enabled (false when unset).
func (c Config) IsQuiet() bool { return c.Quiet != nil && *c.Quiet }

// Worktree configures `rig worktree`. AutoOpen is a pointer so an explicit value
// is distinguishable from "unset" (so a repo's `false` can override a global
// `true` in Merge).
type Worktree struct {
	// AutoOpen controls whether `worktree new` opens the new sibling checkout in
	// a separate window for review. Defaults to false (opt-in).
	AutoOpen *bool `json:"autoOpen,omitempty"`
	// OpenCmd is the command used to open a worktree for review; the worktree
	// path is appended as the final argument. e.g. "code -n" (default),
	// "cursor -n", "subl -n", "idea". Whitespace-split into argv, run directly.
	OpenCmd string `json:"openCmd,omitempty"`
}

// DefaultWorktreeOpenCmd is the command `worktree` uses to open a checkout for
// review when none is configured: a new VS Code window (`code -n <path>`).
var DefaultWorktreeOpenCmd = []string{"code", "-n"}

// WorktreeAutoOpen reports whether `worktree new` should open a review window.
// Defaults to false (opt-in) when unset.
func (c Config) WorktreeAutoOpen() bool {
	return c.Worktree != nil && c.Worktree.AutoOpen != nil && *c.Worktree.AutoOpen
}

// WorktreeOpenCmd returns the configured open command as argv (program + flags),
// or DefaultWorktreeOpenCmd when unset/blank. The worktree path is appended by
// the caller.
func (c Config) WorktreeOpenCmd() []string {
	if c.Worktree != nil {
		if fields := strings.Fields(c.Worktree.OpenCmd); len(fields) > 0 {
			return fields
		}
	}
	return append([]string{}, DefaultWorktreeOpenCmd...)
}

// Test configures the test verb.
type Test struct {
	// Project pins the test project to run.
	Project string `json:"project,omitempty"`
	// EnvPresets are named env bundles, e.g. "log": {"FOO": "1"}, applied by a
	// matching flag (`rig test --log`).
	EnvPresets map[string]map[string]string `json:"envPresets,omitempty"`
}

// Coverage configures the coverage verb.
type Coverage struct {
	Settings  string   `json:"settings,omitempty"`
	Collector string   `json:"collector,omitempty"` // auto | mtp | xplat
	License   string   `json:"license,omitempty"`   // ReportGenerator Pro key
	Open      *bool    `json:"open,omitempty"`      // default: open the report
	Full      *bool    `json:"full,omitempty"`      // default: full multi-file report
	Min       *float64 `json:"min,omitempty"`       // default line-coverage gate

	// ReportGenerator selects how the rich HTML report is produced (the
	// extTool mode for ReportGenerator — see docs/EXTERNAL-TOOLS.md):
	//   "auto"     (default) use ReportGenerator if it's already present
	//              (PATH global tool or local tool-manifest), else the native
	//              per-ecosystem fallback;
	//   "off"      never use ReportGenerator — always the native fallback;
	//   "install"  use ReportGenerator, fetching it on demand (dnx) if missing.
	ReportGenerator string `json:"reportGenerator,omitempty"`
	// ReportTypes overrides ReportGenerator's -reporttypes (default "Html").
	ReportTypes string `json:"reportTypes,omitempty"`
}

// Kill configures `rig kill`'s default (no-arg, no-port) sweep. When Match is
// set it overrides project-name inference outright — the patterns are matched
// against the full command line (pkill -f), so they can also target strays that
// aren't rig projects (a hung test host, an IDE-launched instance).
type Kill struct {
	// Match are command-line substrings to kill on a bare `rig kill`.
	Match []string `json:"match,omitempty"`
}

// Rebuild configures the rebuild verb.
type Rebuild struct {
	// Skip are directory names rebuild leaves alone.
	Skip []string `json:"skip,omitempty"`
}

// Publish configures the publish verb's defaults.
type Publish struct {
	Rid           string `json:"rid,omitempty"`
	SelfContained *bool  `json:"selfContained,omitempty"`
	SingleFile    *bool  `json:"singleFile,omitempty"`
	Output        string `json:"output,omitempty"`
	Configuration string `json:"configuration,omitempty"`
}

// Dotnet is the `dotnet` namespace: .NET-only settings, kept apart from the
// shared, cross-tool keys in a config other rig tools may also read. Folded
// onto the canonical top-level fields by normalize; the namespaced value wins
// when both are present.
type Dotnet struct {
	Solution       string          `json:"solution,omitempty"`
	DefaultProject string          `json:"defaultProject,omitempty"`
	Test           *Test           `json:"test,omitempty"`
	Coverage       *DotnetCoverage `json:"coverage,omitempty"`
	Rebuild        *Rebuild        `json:"rebuild,omitempty"`
	Publish        *Publish        `json:"publish,omitempty"`
	// Formatter selects the .NET formatter `rig format` uses: "csharpier",
	// "dotnet" (dotnet format), or "auto" (default — CSharpier when the repo
	// opts in, else dotnet format). See cli formatter resolution.
	Formatter string `json:"formatter,omitempty"`
}

// DotnetCoverage holds the .NET-only coverage knobs (the open/full/min gate
// lives in the shared top-level `coverage` block).
type DotnetCoverage struct {
	Settings  string `json:"settings,omitempty"`
	Collector string `json:"collector,omitempty"`
	License   string `json:"license,omitempty"`
}

// Load reads the .rig.json at root (typically detect.Root(cwd)). A missing
// file yields a zero-value Config with no error. A present-but-malformed file
// degrades to defaults with a Warning — a broken config shouldn't crash every
// command. Unknown top-level keys are surfaced as Warnings too.
func Load(root string) (Config, error) {
	return loadFile(filepath.Join(root, FileName))
}

// GlobalPath returns the user-wide config location: $RIG_GLOBAL_CONFIG when
// set (the injection seam tests use, mirroring the .NET rig's
// RigSession.GlobalConfigPath), otherwise ~/.rig.json, "" when the home
// directory can't be resolved. Existence isn't checked here — a missing file
// loads as empty.
func GlobalPath() string {
	if custom := os.Getenv("RIG_GLOBAL_CONFIG"); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, FileName)
}

// LoadMerged loads the user-wide ~/.rig.json and the repo's .rig.json at root,
// layering the repo over the global (repo wins per key; see Merge). The merge
// is skipped when the global file IS the repo's file (running rig inside the
// home dir that anchors the repo), matching the .NET rig's RigSession.Load.
func LoadMerged(root string) (Config, error) {
	repo, err := Load(root)
	if err != nil {
		return Config{}, err
	}
	gp := GlobalPath()
	if gp == "" || samePath(gp, filepath.Join(root, FileName)) {
		return repo, nil
	}
	global, err := loadFile(gp)
	if err != nil {
		return Config{}, err
	}
	return Merge(global, repo), nil
}

// samePath reports whether a and b name the same file, comparing absolute
// paths (case-insensitively on the case-insensitive-filesystem platforms).
func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	src := string(data)
	c, perr := Parse(src)
	if perr != nil {
		// A malformed .rig.json shouldn't crash every command — degrade to
		// defaults, but tell the caller why their config went missing.
		return Config{
			Path:     path,
			Warnings: []string{fmt.Sprintf("%s is not valid JSON (%v) — ignoring it", path, perr)},
		}, nil
	}
	c.Path = path
	for _, u := range UnknownKeys(src) {
		w := fmt.Sprintf("%s: unknown key %q", path, u.Key)
		if u.Suggestion != "" {
			w += fmt.Sprintf(" — did you mean %q?", u.Suggestion)
		}
		c.Warnings = append(c.Warnings, w)
	}
	return c, nil
}

// Parse parses JSONC source into a normalized Config. Empty or whitespace-only
// input (e.g. `touch .rig.json`) yields defaults; malformed JSON returns the
// parse error (Load is the tolerant layer).
func Parse(src string) (Config, error) {
	if strings.TrimSpace(src) == "" {
		return Config{}, nil
	}
	var c Config
	if err := jsonc.Unmarshal([]byte(src), &c); err != nil {
		return Config{}, err
	}
	c.normalize()
	return c, nil
}

// normalize folds the new, cross-tool config shape onto the legacy fields
// every verb reads: the `dotnet` namespace collapses onto the top-level
// solution/test/coverage/rebuild/publish fields, and the top-level
// `envPresets` collapses onto test.envPresets. The namespaced value wins when
// both are present, so a repo can migrate to `dotnet.*` without the tool
// changing behavior, and an older .rig.json keeps working unchanged.
// Idempotent; clears the transient inputs so consumers only ever see the
// canonical fields.
func (c *Config) normalize() {
	// Shared `envPresets` wins over the legacy `test.envPresets`.
	if c.EnvPresets != nil {
		if c.Test == nil {
			c.Test = &Test{}
		}
		c.Test.EnvPresets = mergeDict(c.Test.EnvPresets, c.EnvPresets)
		c.EnvPresets = nil
	}

	if d := c.Dotnet; d != nil {
		c.Solution = coalesce(d.Solution, c.Solution)
		c.DefaultProject = coalesce(d.DefaultProject, c.DefaultProject)

		if d.Test != nil {
			if c.Test == nil {
				c.Test = &Test{}
			}
			c.Test.Project = coalesce(d.Test.Project, c.Test.Project)
			c.Test.EnvPresets = mergeDict(c.Test.EnvPresets, d.Test.EnvPresets)
		}

		if d.Coverage != nil {
			if c.Coverage == nil {
				c.Coverage = &Coverage{}
			}
			c.Coverage.Settings = coalesce(d.Coverage.Settings, c.Coverage.Settings)
			c.Coverage.Collector = coalesce(d.Coverage.Collector, c.Coverage.Collector)
			c.Coverage.License = coalesce(d.Coverage.License, c.Coverage.License)
		}

		if d.Rebuild != nil {
			c.Rebuild = d.Rebuild
		}
		if d.Publish != nil {
			c.Publish = d.Publish
		}
		c.DotnetFormatter = coalesce(d.Formatter, c.DotnetFormatter)
		c.Dotnet = nil
	}
}

// Merge layers overlay (the repo's .rig.json) on top of base (the user-wide
// ~/.rig.json): the overlay wins per key, dictionaries union (overlay wins per
// entry), exclude lists union (de-duped, case-insensitive), and
// empty/whitespace strings count as "unset" so a scaffolded blank (e.g.
// coverage.license: "") never shadows a real global value.
func Merge(base, overlay Config) Config {
	kill := base.Kill
	if overlay.Kill.Match != nil {
		kill = overlay.Kill
	}
	rebuild := overlay.Rebuild
	if rebuild == nil {
		rebuild = base.Rebuild
	}
	worktree := overlay.Worktree
	if worktree == nil {
		worktree = base.Worktree
	}
	quiet := overlay.Quiet
	if quiet == nil {
		quiet = base.Quiet
	}
	path := overlay.Path
	if path == "" {
		path = base.Path
	}
	var warnings []string
	warnings = append(warnings, base.Warnings...)
	warnings = append(warnings, overlay.Warnings...)

	return Config{
		Solution:        coalesce(overlay.Solution, base.Solution),
		DefaultProject:  coalesce(overlay.DefaultProject, base.DefaultProject),
		DotnetFormatter: coalesce(overlay.DotnetFormatter, base.DotnetFormatter),
		Ecosystem:       coalesce(overlay.Ecosystem, base.Ecosystem),
		Test:            mergeTest(base.Test, overlay.Test),
		Coverage:        mergeCoverage(base.Coverage, overlay.Coverage),
		Kill:            kill,
		Rebuild:         rebuild,
		Worktree:        worktree,
		Publish:         mergePublish(base.Publish, overlay.Publish),
		Env:             mergeDict(base.Env, overlay.Env),
		Commands:        mergeDict(base.Commands, overlay.Commands),
		Aliases:         mergeDict(base.Aliases, overlay.Aliases),
		Tools:           mergeDict(base.Tools, overlay.Tools),
		Exclude:         mergeList(base.Exclude, overlay.Exclude),
		Quiet:           quiet,
		Path:            path,
		Warnings:        warnings,
	}
}

// coalesce returns overlay unless it is empty/whitespace, then base — a blank
// string counts as "unset" and never shadows a real value.
func coalesce(overlay, base string) string {
	if strings.TrimSpace(overlay) == "" {
		return base
	}
	return overlay
}

func mergeDict[T any](base, overlay map[string]T) map[string]T {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	merged := make(map[string]T, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay { // overlay wins per key
		merged[k] = v
	}
	return merged
}

// mergeList unions the lists, de-duped case-insensitively (first occurrence
// kept) — exclusions accumulate: a personal ~/.rig.json "hide" list adds to
// the repo's.
func mergeList(base, overlay []string) []string {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	seen := make(map[string]bool, len(base)+len(overlay))
	merged := make([]string, 0, len(base)+len(overlay))
	for _, s := range base {
		if k := strings.ToLower(s); !seen[k] {
			seen[k] = true
			merged = append(merged, s)
		}
	}
	for _, s := range overlay {
		if k := strings.ToLower(s); !seen[k] {
			seen[k] = true
			merged = append(merged, s)
		}
	}
	return merged
}

func mergeTest(base, overlay *Test) *Test {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	return &Test{
		Project:    coalesce(overlay.Project, base.Project),
		EnvPresets: mergeDict(base.EnvPresets, overlay.EnvPresets),
	}
}

func mergeCoverage(base, overlay *Coverage) *Coverage {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	merged := &Coverage{
		Settings:  coalesce(overlay.Settings, base.Settings),
		Collector: coalesce(overlay.Collector, base.Collector),
		License:   coalesce(overlay.License, base.License),
		Open:      overlay.Open,
		Full:      overlay.Full,
		Min:       overlay.Min,
	}
	if merged.Open == nil {
		merged.Open = base.Open
	}
	if merged.Full == nil {
		merged.Full = base.Full
	}
	if merged.Min == nil {
		merged.Min = base.Min
	}
	return merged
}

func mergePublish(base, overlay *Publish) *Publish {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	merged := &Publish{
		Rid:           coalesce(overlay.Rid, base.Rid),
		SelfContained: overlay.SelfContained,
		SingleFile:    overlay.SingleFile,
		Output:        coalesce(overlay.Output, base.Output),
		Configuration: coalesce(overlay.Configuration, base.Configuration),
	}
	if merged.SelfContained == nil {
		merged.SelfContained = base.SelfContained
	}
	if merged.SingleFile == nil {
		merged.SingleFile = base.SingleFile
	}
	return merged
}

// knownKeys are the top-level keys rig recognizes. "node" is another rig
// tool's namespace in a shared config — accepted (ignored), never flagged.
// "ecosystem" is the Go rig's addition for cross-ecosystem resolution.
var knownKeys = []string{
	"$schema", "solution", "defaultProject", "ecosystem", "test", "coverage",
	"kill", "rebuild", "publish", "env", "envPresets", "commands", "aliases",
	"tools", "exclude", "quiet", "dotnet", "node", "worktree",
}

// UnknownKey is a top-level key rig doesn't recognize, with the closest known
// key as a did-you-mean suggestion ("" when nothing is plausibly close).
type UnknownKey struct {
	Key        string
	Suggestion string
}

// UnknownKeys returns the top-level keys in the JSONC source that rig doesn't
// recognize (typos). The parser silently ignores them, so this surfaces them
// (with a "did you mean" guess) for `rig info`. Never fails: malformed JSON is
// the loader's problem, not ours — it yields an empty list.
func UnknownKeys(src string) []UnknownKey {
	var doc map[string]any
	if err := jsonc.Unmarshal([]byte(src), &doc); err != nil || doc == nil {
		return nil
	}
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	sort.Strings(names)

	var unknown []UnknownKey
	for _, name := range names {
		if isKnownKey(name) {
			continue
		}
		unknown = append(unknown, UnknownKey{Key: name, Suggestion: closestKey(name)})
	}
	return unknown
}

func isKnownKey(name string) bool {
	for _, k := range knownKeys {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// closestKey returns the known key nearest to key by edit distance, "" when
// nothing is within distance 3 (only suggest a plausibly-close match).
func closestKey(key string) string {
	best, bestDistance := "", int(^uint(0)>>1)
	for _, known := range knownKeys {
		if d := levenshtein(strings.ToLower(key), strings.ToLower(known)); d < bestDistance {
			best, bestDistance = known, d
		}
	}
	if bestDistance <= 3 {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
