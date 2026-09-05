// Package config models clauderig's configuration — the remote, the per-machine
// path maps (the single source of truth pathmap reads), the sync roots and their
// per-OS locations, and retention. Machine maps live here so a synced session
// translates to whatever layout each machine uses.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/jsonc"
	"github.com/rigsmith/rigsmith/core/pathmap"
)

const schemaVersion = 1

// DefaultMaxFileBytes is the per-file size cap: GitHub warns above 50 MB and
// rejects the whole push above 100 MB, so cap under the warning rather than at
// the cliff. Only runaway transcripts come near it.
const DefaultMaxFileBytes = 50 << 20

// DefaultLargeFileBytes is where a transcript stops being restaged on every
// sync. A session transcript is append-only and unbounded, and committing the
// whole file each interval costs the repo roughly (size × syncs) / 2 — quadratic
// in session length — so past this size a transcript is restaged only once it
// has grown by half this much again, or has gone quiet (see engine.Sync).
const DefaultLargeFileBytes = 8 << 20

// SchemaURL is stamped onto written config.json files, matching the other
// rigsmith configs (.rig.json, .changeset/config.json).
const SchemaURL = "https://rigsmith.dev/schemas/clauderig.json"

// writer renders config.json as a schema-stamped JSONC document — consistent
// with rig/changerig/shiprig, which all write (and read) JSONC.
var writer = confkit.Writer{SchemaURL: SchemaURL}

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
	// MaxFileBytes drops any single file bigger than this (0 = no cap). A marathon
	// transcript can reach hundreds of MB, and git hosts reject those outright —
	// GitHub warns past 50 MB and refuses past 100 MB, which fails the whole push.
	// One runaway session must not be able to wedge the sync.
	MaxFileBytes int64 `json:"maxFileBytes"`
	// LargeFileBytes is the size past which a transcript is restaged only when
	// it has grown by at least half this much since the staged copy, or has not
	// been written for a while (0 = the default; negative = restage every
	// change). It bounds how many near-identical blobs a long session leaves in
	// history without delaying small sessions at all.
	LargeFileBytes int64 `json:"largeFileBytes,omitempty"`
	// SquashKeepDays is how many whole days of sync history the automatic squash
	// leaves standing. 0 means the default (DefaultSquashKeepDays).
	//
	// The squash used to collapse everything to a single commit, so the moment
	// it fired became the beginning of recorded history — at whatever o'clock
	// that happened to be. Keeping a week means there is still something to read
	// afterwards, and cutting on a day boundary means the answer to "how far
	// back do I go" is a date rather than a timestamp.
	SquashKeepDays int `json:"squashKeepDays,omitempty"`
}

// HookInterval is HookIntervalMinutes as a duration: unset means the default,
// 0 means no debounce at all. A negative value is read as 0 rather than
// rejected — someone who wrote -1 meant "off", and refusing to sync over a sign
// would be a poor way to tell them.
func (c *Config) HookInterval() time.Duration {
	if c.HookIntervalMinutes == nil {
		return DefaultHookInterval
	}
	if *c.HookIntervalMinutes <= 0 {
		return 0
	}
	return time.Duration(*c.HookIntervalMinutes) * time.Minute
}

// DefaultHookIntervalMinutes is the debounce applied to hook-driven syncs when
// the config does not say otherwise.
const DefaultHookIntervalMinutes = 5

// DefaultHookInterval is DefaultHookIntervalMinutes as a duration.
const DefaultHookInterval = DefaultHookIntervalMinutes * time.Minute

// DefaultSquashKeepDays is the history the automatic squash retains when the
// config does not say.
//
// Generous on purpose. Once loose objects are packed before the size is judged,
// the squash almost never fires — a repo whose .git looked like 2.9 GB was 559
// MB of actual history against a 3.2 GB threshold — so when it does fire the
// right instinct is to keep a month rather than to cut to the bone. If a month
// is not enough relief, nothing is folded again and the ratio simply stays
// visible, which is a better outcome than a tool quietly eating history until
// the number looks nice.
const DefaultSquashKeepDays = 30

// KeepDays is SquashKeepDays with the default applied.
func (r Retention) KeepDays() int {
	if r.SquashKeepDays <= 0 {
		return DefaultSquashKeepDays
	}
	return r.SquashKeepDays
}

// Root is a sync root: an id, whether it's enabled, and its per-OS location as a
// cascade of portable templates (resolved against a machine's home/OS).
type Root struct {
	ID       string          `json:"id"`
	Enabled  bool            `json:"enabled"`
	Location pathmap.Cascade `json:"location"`
}

// Config is the clauderig configuration document.
type Config struct {
	// ChunkTranscripts overrides repository storage mode. New configs default
	// to true; an omitted key in existing configs means auto (follow the repo).
	ChunkTranscripts *bool              `json:"chunkTranscripts,omitempty"`
	Schema           int                `json:"schema"`
	Remote           string             `json:"remote,omitempty"`
	Machines         map[string]Machine `json:"machines"`
	Roots            []Root             `json:"roots"`
	Retention        Retention          `json:"retention"`
	// AlwaysPrune makes `restore` prune stale config files (skills/commands/
	// agents/plans deleted upstream) by default, as if --prune were passed.
	// `restore --prune=false` overrides it for a single run.
	AlwaysPrune bool `json:"alwaysPrune,omitempty"`
	// HookIntervalMinutes is how long a hook-driven sync waits before doing the
	// work again. The Stop hook fires at the end of every turn in every chat, so
	// several open conversations meant the same tree walked, redacted and pushed
	// several times a minute to write one changed file.
	//
	// Minutes because that is the unit the decision is made in — "sync at most
	// every few minutes" — and seconds invited a number precise enough to imply
	// a control this does not have.
	//
	// 0 turns the debounce off and syncs on every turn, which is what happened
	// before this existed. A POINTER because "0" and "absent" have to mean
	// different things: unset is the default interval, and a plain int cannot
	// tell someone who wrote 0 from someone who wrote nothing.
	//
	// Only hook-driven runs are affected — `clauderig sync` typed by hand always
	// does the work.
	HookIntervalMinutes *int `json:"hookIntervalMinutes,omitempty"`
	// RedactTranscripts scrubs credential-shaped tokens out of the STAGED copy of
	// a transcript before it is committed. The live ~/.claude file is never
	// touched — clauderig backs your machine up, it does not edit it — so the
	// secret stays where you left it. The publication scan runs whether or not
	// this scrubber is enabled, refusing recognized credentials that remain.
	//
	// Off by default. It rewrites the middle of a conversation, which is a thing
	// a backup tool should do only because you asked it to.
	RedactTranscripts bool `json:"redactTranscripts,omitempty"`
	// AutoRestore makes the SessionStart hook (`clauderig pull`) also restore on a
	// FRESH machine (no projects yet) — auto-wiring a new computer. It deliberately
	// never restores over an established machine (would churn/clobber).
	AutoRestore bool `json:"autoRestore,omitempty"`
}

// Default returns a config with the standard roots and retention, no machines or
// remote yet (init fills those).
func Default() *Config {
	chunked := true
	return &Config{
		ChunkTranscripts: &chunked,
		Schema:           schemaVersion,
		Machines:         map[string]Machine{},
		Roots:            DefaultRoots(),
		Retention:        Retention{HistoryDays: 90, SquashFactor: 2.0, FloorBytes: 500 << 20, MaxFileBytes: DefaultMaxFileBytes, LargeFileBytes: DefaultLargeFileBytes},
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
			return r.ResolveOn(m)
		}
	}
	return "", pathmap.StatusInvalid
}

// ResolveOn resolves this root's location on machine m. Takes the root by value
// rather than by id so a root synthesized at runtime — one per Claude Desktop
// profile — resolves the same way as one read from the config file.
func (r Root) ResolveOn(m Machine) (string, pathmap.Status) {
	res := m.Resolver().Resolve(r.Location.RawFor(m.OS))
	return res.Path, res.Status
}

// Detect builds a Machine for the host this binary runs on.
func Detect(name string) Machine {
	home, _ := os.UserHomeDir()
	return Machine{Name: name, OS: OSToken(), Home: home}
}

// DetectFor builds a Machine for this host, named by ResolveName — the form
// every caller wants, and the one that keeps the CLI and the UI agreeing about
// which registry entry is "this machine".
func DetectFor(cfg *Config) Machine { return Detect(ResolveName(cfg)) }

// UnresolvedName is the placeholder used when this machine has no stable
// identity — no matching config entry and no usable hostname.
//
// It is load-bearing history: a hostname that failed to resolve once registered
// a ghost device literally named "this", which sat in the synced registry from
// June 2026 until it was removed by hand on 2026-08-07. Display paths still
// need *some* name, so the placeholder stays — but anything that *writes* an
// identity must check IdentityResolved first and decline.
const UnresolvedName = "this"

// ResolveName picks this machine's name: an existing config entry matching this
// OS+home, else the hostname, else UnresolvedName.
func ResolveName(cfg *Config) string {
	name, _ := resolveName(cfg)
	return name
}

// IdentityResolved reports whether this machine has a real, stable identity —
// false when ResolveName fell all the way through to UnresolvedName.
//
// Registration is gated on this: writing a placeholder into the synced device
// registry creates a ghost every other machine then has to look at.
func IdentityResolved(cfg *Config) bool {
	_, ok := resolveName(cfg)
	return ok
}

func resolveName(cfg *Config) (string, bool) {
	localOS := OSToken()
	home, _ := os.UserHomeDir()

	names := make([]string, 0, len(cfg.Machines))
	for name := range cfg.Machines {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order if several entries somehow match
	for _, name := range names {
		if m := cfg.Machines[name]; m.OS == localOS && m.Home == home {
			return name, true
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host, true
	}
	return UnresolvedName, false
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

// Save writes the config to dir/config.json as a schema-stamped JSONC document
// (a leading comment + $schema), consistent with the other rigsmith tools. The
// loader reads JSONC, so hand-added comments survive a round-trip parse (a
// full-struct rewrite here doesn't preserve them — config.json is tool-managed).
func Save(c *Config, dir string) error {
	b, err := writer.Document("clauderig config — JSONC: comments and trailing commas are allowed.", c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), b, 0o644)
}

// Load reads dir/config.json.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	// JSONC: tolerate comments/trailing commas, consistent with the other
	// rigsmith config files (and with what Save now emits).
	if err := jsonc.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	// A config written before the size cap existed has no maxFileBytes; absent must
	// mean "the default", not "no cap", or the configs that most need the cap (long
	// history, pre-existing) are exactly the ones that never get it. Disabling is
	// explicit: any negative value.
	if c.Retention.MaxFileBytes == 0 {
		c.Retention.MaxFileBytes = DefaultMaxFileBytes
	}
	// Same reasoning: the throttle exists for repos that already have long
	// sessions in them, so absent means the default.
	if c.Retention.LargeFileBytes == 0 {
		c.Retention.LargeFileBytes = DefaultLargeFileBytes
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

// DesktopConfigKeepKeys are the top-level keys of Desktop's config.json that are
// both stable and portable — the only ones safe to carry between machines or
// between profiles.
//
// Everything else in that file is volatile or identity-bearing: `oauth:*` holds
// the login itself, `lastKnownAccountUuid` names the account it belongs to, and
// `dxt:*`, `updaterLastSeenVersion` and `first_launch_at` are caches and machine
// state. Keep this list conservative — an omission costs coverage, a wrong entry
// costs safety.
//
// Shared by `clauderig sync` (which prunes the synced copy to these keys) and by
// `clauderig desktop add`'s profile seeding, so the two can never disagree about
// what is safe to copy.
func DesktopConfigKeepKeys() []string {
	return []string{"preferences", "locale", "userThemeMode"}
}
