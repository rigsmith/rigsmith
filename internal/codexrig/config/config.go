// Package config models codexrig's configuration — the remote, the per-machine
// path maps (the single source of truth pathmap reads), the sync roots and their
// per-OS locations, and retention. Machine maps live here so a synced session
// translates to whatever layout each machine uses.
//
// It is deliberately a near-mirror of clauderig's config: the two tools are
// siblings, someone who runs both should not have to learn two vocabularies, and
// a shared key name is a promise that the two mean the same thing. Where Codex's
// own nouns differ (it calls a session transcript a "rollout") the doc comment
// says so rather than the key.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/jsonc"
	"github.com/rigsmith/rigsmith/core/pathmap"
)

const schemaVersion = 1

// DefaultMaxFileBytes is the per-file size cap: GitHub warns above 50 MB and
// rejects the whole push above 100 MB, so cap under the warning rather than at
// the cliff. Only runaway rollouts come near it.
const DefaultMaxFileBytes = 50 << 20

// DefaultLargeFileBytes is where a rollout stops being restaged on every sync.
// A rollout is append-only and unbounded, and committing the whole file each
// interval costs the repo roughly (size × syncs) / 2 — quadratic in session
// length — so past this size a rollout is restaged only once it has grown by
// half this much again, or has gone quiet.
const DefaultLargeFileBytes = 8 << 20

// SchemaURL is stamped onto written config.json files, matching the other
// rigsmith configs (.rig.json, .changeset/config.json, clauderig's config.json).
const SchemaURL = "https://rigsmith.dev/schemas/codexrig.json"

// writer renders config.json as a schema-stamped JSONC document — consistent
// with rig/changerig/shiprig/clauderig, which all write (and read) JSONC.
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
		// HOME is detected, never configured: a token by that name would
		// redirect every $HOME template — the default root, and so the
		// restore target — to wherever config.json said.
		if strings.EqualFold(k, "HOME") {
			continue
		}
		f[k] = v
	}
	return f
}

// Resolver returns a pathmap resolver that expands portable templates into this
// machine's native paths.
func (m Machine) Resolver() *pathmap.Resolver {
	return pathmap.NewResolver(m.Folders(), m.OS, nil)
}

// Retention controls the rollout-history window and when the history orphan
// branch is squashed (size-based: squash when the branch's git footprint exceeds
// Factor × the retained working-tree size, but never below FloorBytes).
type Retention struct {
	HistoryDays  int     `json:"historyDays"`
	SquashFactor float64 `json:"squashFactor"`
	FloorBytes   int64   `json:"floorBytes"`
	// MaxFileBytes drops any single file bigger than this (0 = the default,
	// negative = no cap). Git hosts reject oversized blobs and take the whole
	// push down with them; one runaway session must not wedge the sync.
	MaxFileBytes int64 `json:"maxFileBytes"`
	// LargeFileBytes is the size past which a rollout is restaged only when it
	// has grown by at least half this much since the staged copy, or has not
	// been written for a while (0 = the default; negative = restage every
	// change).
	LargeFileBytes int64 `json:"largeFileBytes,omitempty"`
	// SquashKeepDays is how many whole days of sync history the automatic squash
	// leaves standing. 0 means DefaultSquashKeepDays.
	SquashKeepDays int `json:"squashKeepDays,omitempty"`
}

// DefaultHookIntervalMinutes is the debounce applied to hook-driven syncs when
// the config does not say otherwise.
const DefaultHookIntervalMinutes = 5

// DefaultHookInterval is DefaultHookIntervalMinutes as a duration.
const DefaultHookInterval = DefaultHookIntervalMinutes * time.Minute

// DefaultSquashKeepDays is the history the automatic squash retains when the
// config does not say.
const DefaultSquashKeepDays = 30

// KeepDays is SquashKeepDays with the default applied.
func (r Retention) KeepDays() int {
	if r.SquashKeepDays <= 0 {
		return DefaultSquashKeepDays
	}
	return r.SquashKeepDays
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

// Root is a sync root: an id, whether it's enabled, and its per-OS location as a
// cascade of portable templates (resolved against a machine's home/OS).
type Root struct {
	ID       string          `json:"id"`
	Enabled  bool            `json:"enabled"`
	Location pathmap.Cascade `json:"location"`
}

// RootCLI is the id of the Codex CLI root ($HOME/.codex). It is the only root
// codexrig ships with: Codex's desktop app keeps no portable state of its own
// that has been shown to survive a restore, so there is deliberately no
// `desktop` root to imply otherwise.
const RootCLI = "cli"

// Config is the codexrig configuration document.
type Config struct {
	Schema   int                `json:"schema"`
	Remote   string             `json:"remote,omitempty"`
	Machines map[string]Machine `json:"machines"`
	Roots    []Root             `json:"roots"`

	Retention Retention `json:"retention"`

	// AlwaysPrune makes `restore` prune stale config files (prompts, skills,
	// hooks deleted upstream) by default, as if --prune were passed.
	AlwaysPrune bool `json:"alwaysPrune,omitempty"`

	// HookIntervalMinutes is how long a hook-driven sync waits before doing the
	// work again. Codex's Stop hook fires at the end of every turn in every
	// session, so several open sessions would otherwise mean the same tree
	// walked, redacted and pushed several times a minute to write one changed
	// file.
	//
	// A POINTER because "0" and "absent" have to mean different things: unset is
	// the default interval, and a plain int cannot tell someone who wrote 0 from
	// someone who wrote nothing.
	HookIntervalMinutes *int `json:"hookIntervalMinutes,omitempty"`

	// RedactTranscripts scrubs credential-shaped tokens out of the STAGED copy
	// of a rollout before it is committed. The live ~/.codex file is never
	// touched — codexrig backs your machine up, it does not edit it — so the
	// secret stays where you left it. The publication scan runs whether or not
	// this scrubber is enabled, refusing recognized credentials that remain.
	//
	// Off by default. It rewrites the middle of a conversation, which is a thing
	// a backup tool should do only because you asked it to.
	RedactTranscripts bool `json:"redactTranscripts,omitempty"`

	// ChunkRollouts stores a large rollout in the repo as content-addressed
	// parts rather than as one blob, so an append costs a chunk instead of a
	// copy. A POINTER, because absent has to mean something different from
	// false: absent follows whatever the repo is already doing, which is what
	// lets a second machine join a fleet without being told.
	ChunkRollouts *bool `json:"chunkRollouts,omitempty"`

	// SyncSessions carries rollout files (Codex's session transcripts) as well
	// as configuration. Separate from clauderig, where transcripts are always
	// in: Codex rollouts are the artifact whose cross-machine resume is NOT yet
	// a proven round trip, so carrying them is backup, not portability, and the
	// user gets to say whether a backup that large is what they wanted.
	SyncSessions bool `json:"syncSessions,omitempty"`

	// AutoRestore makes the SessionStart hook (`codexrig pull`) also restore on
	// a FRESH machine (no Codex config yet) — auto-wiring a new computer. It
	// never restores over an established machine.
	AutoRestore bool `json:"autoRestore,omitempty"`
}

// Default returns a config with the standard root and retention, no machines or
// remote yet (init fills those).
func Default() *Config {
	return &Config{
		// ChunkRollouts stays nil here on purpose. The field's contract is that
		// absent differs from false — a machine with no opinion follows what
		// the repo already does — and Default() is exactly the machine with no
		// opinion. Setting true here made the tri-state unreachable.
		Schema:    schemaVersion,
		Machines:  map[string]Machine{},
		Roots:     DefaultRoots(),
		Retention: Retention{HistoryDays: 90, SquashFactor: 2.0, FloorBytes: 500 << 20, MaxFileBytes: DefaultMaxFileBytes, LargeFileBytes: DefaultLargeFileBytes},
		// On by default, unlike the sessions themselves: once someone opts into
		// carrying conversation text, scrubbing it is the behaviour they meant.
		RedactTranscripts: true,
	}
}

// DefaultRoots is the Codex CLI root. It is identical on every OS — Codex keeps
// its home at $HOME/.codex everywhere, and relocates it only through CODEX_HOME,
// which is a per-process choice and therefore not a machine's layout.
func DefaultRoots() []Root {
	return []Root{
		{ID: RootCLI, Enabled: true, Location: pathmap.Cascade{Portable: "$HOME/.codex"}},
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

// ResolveOn resolves this root's location on machine m.
func (r Root) ResolveOn(m Machine) (string, pathmap.Status) {
	res := m.Resolver().Resolve(r.Location.RawFor(m.OS))
	return res.Path, res.Status
}

// Detect builds a Machine for the host this binary runs on.
func Detect(name string) Machine {
	home, _ := os.UserHomeDir()
	return Machine{Name: name, OS: OSToken(), Home: home}
}

// DetectFor is Detect for a machine this config may already know about: the
// live OS and home, plus the custom folder tokens the config entry records.
//
// It used to be Detect(ResolveName(cfg)), which took only the NAME from the
// config and rebuilt everything else from the host — so Machine.Tokens, the one
// field that exists solely to be configured, never reached Folders(), and
// every command portablized against HOME alone. clauderig had the same hole.
func DetectFor(cfg *Config) Machine {
	name := ResolveName(cfg)
	m := Detect(name)
	if cfg != nil {
		if known, ok := cfg.Machines[name]; ok && len(known.Tokens) > 0 {
			// A copy: the caller may edit what it was handed, and sharing the
			// map would edit the config it came from.
			m.Tokens = make(map[string]string, len(known.Tokens))
			for k, v := range known.Tokens {
				m.Tokens[k] = v
			}
		}
	}
	return m
}

// UnresolvedName is the placeholder used when this machine has no stable
// identity — no matching config entry and no usable hostname. Anything that
// WRITES an identity must check IdentityResolved first and decline: clauderig
// once registered a ghost device literally named "this" that sat in a synced
// registry for two months.
const UnresolvedName = "this"

// ResolveName picks this machine's name: an existing config entry matching this
// OS+home, else the hostname, else UnresolvedName.
func ResolveName(cfg *Config) string {
	name, _ := resolveName(cfg)
	return name
}

// IdentityResolved reports whether this machine has a real, stable identity.
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

// Save writes the config to dir/config.json as a schema-stamped JSONC document.
func Save(c *Config, dir string) error {
	b, err := writer.Document("codexrig config — JSONC: comments and trailing commas are allowed.", c)
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
	if err := jsonc.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	// Stamped on every write and, until now, never read. A config from a newer
	// codexrig can rename or reinterpret a field; parsing it as this version
	// silently reads the old meaning.
	if c.Schema > schemaVersion {
		return nil, fmt.Errorf("config.json is schema %d, and this codexrig understands up to %d — upgrade codexrig", c.Schema, schemaVersion)
	}
	// pathmap treats every OS token that is not "windows" as POSIX, so a typo
	// here does not fail, it resolves paths for the wrong platform. Fail closed.
	for name, m := range c.Machines {
		switch m.OS {
		case "macos", "windows", "linux", "":
		default:
			return nil, fmt.Errorf("machine %q has os %q; it must be macos, windows or linux", name, m.OS)
		}
	}
	// A config written before the size cap existed has no maxFileBytes; absent
	// must mean "the default", not "no cap", or the configs that most need the
	// cap are exactly the ones that never get it. Disabling is explicit: any
	// negative value.
	if c.Retention.MaxFileBytes == 0 {
		c.Retention.MaxFileBytes = DefaultMaxFileBytes
	}
	if c.Retention.LargeFileBytes == 0 {
		c.Retention.LargeFileBytes = DefaultLargeFileBytes
	}
	return &c, nil
}

// Dir is codexrig's config directory (~/.codexrig), where config.json lives.
func Dir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".codexrig"), nil
}

// StagingDir is the local staging repo (~/.codexrig/repo) that sync pushes from.
//
// Deliberately its own repo, not a second namespace inside clauderig's: the
// assessment's rule is that shipping the Codex tool must not make an existing
// clauderig user migrate their backup layout.
func StagingDir() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "repo"), nil
}

// LoadOrDefault loads the saved config, falling back to Default ONLY when no
// config file exists. A present-but-corrupt config.json is surfaced rather than
// silently replaced with defaults.
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
