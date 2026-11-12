// Package secrets is the seam between the rigsmith tools and a secrets
// manager. A repo opts in with one block in `.rig.json` naming the manager's
// project and the config each context reads:
//
//	"doppler": { "project": "foxcraft", "dev": "dev", "ci": "ci", "release": "release" }
//
// Absent means no secrets manager, and nothing changes. The block's key names
// the Provider, so a second manager (Infisical, say) is one more Provider and
// one more key, with nothing to change in the tools that use this package.
//
// Three rules hold for every provider, because each was learned the hard way:
//
//   - A value never reaches output. Errors carry names, scopes and the
//     manager's own diagnostics, never what a fetch printed on stdout.
//   - Nothing is ever silently empty. A missing CLI, a signed-out CLI, a
//     missing secret and an empty secret are all errors — an empty Postgres
//     connection once let every Postgres test lane skip while the run showed
//     green.
//   - The project and config are named on every call. A manager's per-folder
//     setup (doppler.yaml, `doppler setup`) does not survive a new git
//     worktree, which is a new folder every time.
package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// Context is the purpose a run reads secrets for; each maps to one config in
// the provider's project.
type Context string

const (
	// Dev is a developer's own machine: `rig test`, `rig run`.
	Dev Context = "dev"
	// CI is a GitHub Actions job, which receives its secrets from the
	// workflow's fetch step rather than a CLI.
	CI Context = "ci"
	// Release is publishing: feed keys, signing material, notary credentials.
	Release Context = "release"
)

// Settings is one provider's `.rig.json` block.
type Settings struct {
	// Project is the provider's project for this product (e.g. "foxcraft").
	Project string `json:"project,omitempty"`
	// Dev, CI and Release name the config each context reads. Unset means
	// that context reads no secrets.
	Dev     string `json:"dev,omitempty"`
	CI      string `json:"ci,omitempty"`
	Release string `json:"release,omitempty"`
}

// Config returns the config name for c, "" when that context is not set.
func (s *Settings) Config(c Context) string {
	if s == nil {
		return ""
	}
	switch c {
	case Dev:
		return strings.TrimSpace(s.Dev)
	case CI:
		return strings.TrimSpace(s.CI)
	case Release:
		return strings.TrimSpace(s.Release)
	}
	return ""
}

// Scope returns the project/config pair for c, and false when c has no
// config.
func (s *Settings) Scope(c Context) (Scope, bool) {
	cfg := s.Config(c)
	if s == nil || cfg == "" {
		return Scope{}, false
	}
	return Scope{Project: strings.TrimSpace(s.Project), Config: cfg}, true
}

// identifier is what a project or config name may hold. Every value lands in
// a command line after a flag, so one starting with "-" could be read as a
// flag of its own; this rules that out along with spaces and quotes.
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// secretName is what a secret name may hold: the environment-variable shape
// every manager uses, and the only shape that is safe to splice into a
// GitHub Actions expression.
var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidName reports whether name is a usable secret name.
func ValidName(name string) bool { return secretName.MatchString(name) }

// Validate reports the first problem with the block: a missing project, or a
// project or config name that is not a plain identifier.
func (s *Settings) Validate(key string) error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(s.Project) == "" {
		return fmt.Errorf("%s.project is required", key)
	}
	fields := []struct{ name, value string }{
		{"project", s.Project}, {"dev", s.Dev}, {"ci", s.CI}, {"release", s.Release},
	}
	for _, f := range fields {
		v := strings.TrimSpace(f.value)
		if v != "" && !identifier.MatchString(v) {
			return fmt.Errorf("%s.%s %q is not a plain name (letters, digits, '_', '-', '.')", key, f.name, f.value)
		}
	}
	return nil
}

// Scope is one project/config pair: everything a provider needs to name the
// secrets on a call.
type Scope struct {
	Project string
	Config  string
}

// String renders the scope as "project/config".
func (s Scope) String() string { return s.Project + "/" + s.Config }

// Provider is a secrets manager the tools can read from. Implementations shell
// out to the manager's own CLI, so a developer's sign-in is the manager's and
// rigsmith stores nothing.
type Provider interface {
	// Name is the `.rig.json` key and the secret-reference prefix
	// ("doppler" → `"doppler": {…}` and `doppler:NAME`).
	Name() string
	// Title is the name people know it by, for messages ("Doppler").
	Title() string
	// Ready reports whether the CLI is installed and signed in for dir. The
	// error says how to fix it: wrap ErrNotInstalled or ErrSignedOut.
	Ready(ctx context.Context, dir string) error
	// Wrap returns argv run under the manager, with scope's secrets in its
	// environment (`doppler run -p P -c C -- argv…`).
	Wrap(scope Scope, argv []string) []string
	// Get reads one secret. A missing or empty secret is an error
	// (ErrNotFound / ErrEmpty), and no error carries a value.
	Get(ctx context.Context, dir string, scope Scope, name string) (string, error)
	// All reads every secret in scope, for a run rig cannot wrap (a command
	// it interprets in-process).
	All(ctx context.Context, dir string, scope Scope) (map[string]string, error)
}

var (
	// ErrNotInstalled is a provider whose CLI is not on PATH.
	ErrNotInstalled = errors.New("CLI not installed")
	// ErrSignedOut is a provider CLI with no credential for this folder.
	ErrSignedOut = errors.New("CLI not signed in")
	// ErrNotFound is a secret the scope does not hold.
	ErrNotFound = errors.New("secret not found")
	// ErrEmpty is a secret that is set to nothing.
	ErrEmpty = errors.New("secret is empty")
)

// providers are the managers a `.rig.json` block can name, by key. A
// variable so tests can substitute a fake.
var providers = map[string]func() Provider{
	"doppler": func() Provider { return NewDoppler() },
}

// Keys lists the `.rig.json` keys that name a provider, sorted.
func Keys() []string {
	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ProviderFor returns the provider a key names, or nil.
func ProviderFor(key string) Provider {
	if mk, ok := providers[key]; ok {
		return mk()
	}
	return nil
}

// SetProviderForTest replaces the provider for key until the returned restore
// runs. For tests outside this package that must not reach a real CLI.
func SetProviderForTest(key string, p Provider) (restore func()) {
	prev, had := providers[key]
	providers[key] = func() Provider { return p }
	return func() {
		if had {
			providers[key] = prev
		} else {
			delete(providers, key)
		}
	}
}

// Setup is a repo's chosen provider and its settings.
type Setup struct {
	Provider Provider
	Settings Settings
}

// Scope returns the scope for c, and false when c has no config.
func (s *Setup) Scope(c Context) (Scope, bool) {
	if s == nil {
		return Scope{}, false
	}
	return s.Settings.Scope(c)
}

// Select picks the provider from the blocks a config holds (key → block, nil
// for an absent one). No block means no provider (nil, nil); two blocks is an
// error, since a run can read from one place; a block that does not validate
// is an error rather than a silent opt-out.
func Select(blocks map[string]*Settings) (*Setup, error) {
	var found []string
	for key, b := range blocks {
		if b != nil {
			found = append(found, key)
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, fmt.Errorf("%s each name a secrets manager — keep one", strings.Join(found, " and "))
	}
	key := found[0]
	p := ProviderFor(key)
	if p == nil {
		return nil, fmt.Errorf("%q is not a secrets manager rig knows (want %s)", key, strings.Join(Keys(), ", "))
	}
	settings := *blocks[key]
	if err := settings.Validate(key); err != nil {
		return nil, err
	}
	return &Setup{Provider: p, Settings: settings}, nil
}

// Load reads the provider blocks from the `.rig.json` at root and selects one.
// A missing file, or one with no provider block, is (nil, nil). It is for the
// tools that do not otherwise read `.rig.json` (shiprig); rig decodes the
// block with the rest of its config and calls Select.
func Load(root string) (*Setup, error) {
	return LoadFile(filepath.Join(root, ".rig.json"))
}

// LoadLayered reads the repo's `.rig.json` at root and, when it names no
// provider, the user-wide config at global ("" skips it) — the layering rig
// applies to every key. It is the loader both rig and shiprig decide a run's
// secrets with, so a block is read the same strict way by each.
func LoadLayered(root, global string) (*Setup, error) {
	setup, err := Load(root)
	if err != nil || setup != nil || global == "" {
		return setup, err
	}
	return LoadFile(global)
}

// settingsKeys are the keys a provider block may hold.
var settingsKeys = []string{"project", "dev", "ci", "release"}

// LoadFile is Load for one config file by path — the user-wide ~/.rig.json,
// say, which rig layers under every repo's. It is strict where a lenient read
// would quietly run without secrets: a file that exists but can't be read, a
// file that can't be parsed but names a provider, a provider block given
// twice (keys differ only by case), and a key inside a block that isn't one
// of project/dev/ci/release (a "releasee" typo would otherwise read as "no
// release config" and send a release to the keychain). A file that can't be
// parsed and names no provider is not this package's to judge: (nil, nil).
func LoadFile(path string) (*Setup, error) {
	name := filepath.Base(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var doc map[string]jsonRaw
	if err := jsonc.Unmarshal(data, &doc); err != nil {
		if mentionsProvider(string(data)) {
			return nil, fmt.Errorf("%s is not valid JSON (%v), and it names a secrets manager — fix it rather than run without secrets", path, err)
		}
		return nil, nil
	}
	// encoding/json keeps only the last of two identical keys, so a doubled
	// "doppler" never reaches lookupFold; count the keys as written.
	if err := rejectRepeatedProviderKeys(data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	blocks := map[string]*Settings{}
	for _, key := range Keys() {
		raw, err := lookupFold(doc, key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if raw == nil || string(raw) == "null" {
			continue
		}
		var fields map[string]jsonRaw
		if err := jsonc.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, key, err)
		}
		for field := range fields {
			if !knownSettingsKey(field) {
				return nil, fmt.Errorf("%s: %s.%s is not a key (want %s)", name, key, field, strings.Join(settingsKeys, ", "))
			}
		}
		var s Settings
		if err := jsonc.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, key, err)
		}
		blocks[key] = &s
	}
	return Select(blocks)
}

func knownSettingsKey(field string) bool {
	for _, k := range settingsKeys {
		if strings.EqualFold(k, field) {
			return true
		}
	}
	return false
}

// mentionsProvider reports whether unparseable config text names a provider
// key, so a broken file that may hold a block is refused rather than skipped.
func mentionsProvider(text string) bool {
	low := strings.ToLower(text)
	for _, key := range Keys() {
		if strings.Contains(low, `"`+key+`"`) {
			return true
		}
	}
	return false
}

// jsonRaw defers decoding a block until its key is known to name a provider.
type jsonRaw []byte

func (r *jsonRaw) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// rejectRepeatedProviderKeys scans the top-level keys of JSONC data as
// written and refuses a provider key that appears more than once, in any
// spelling. The decoded map can't show it: the last copy silently wins.
func rejectRepeatedProviderKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(jsonc.Strip(data)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil // not an object; the decode already judged it
	}
	seen := map[string]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil
		}
		for _, p := range Keys() {
			if strings.EqualFold(key, p) {
				if seen[p]++; seen[p] > 1 {
					return fmt.Errorf("the %q block is set more than once — keep one", p)
				}
			}
		}
	}
	return nil
}

// lookupFold finds key in doc case-insensitively, as the rest of `.rig.json`
// is matched. Two spellings of one key are an error: which one won would
// depend on map order, so the credential scope could change between runs.
func lookupFold(doc map[string]jsonRaw, key string) ([]byte, error) {
	var found []string
	var value []byte
	for k, v := range doc {
		if strings.EqualFold(k, key) {
			found = append(found, k)
			value = v
		}
	}
	if len(found) > 1 {
		sort.Strings(found)
		return nil, fmt.Errorf("%s each set the %q block — keep one", strings.Join(found, " and "), key)
	}
	return value, nil
}

// InCI reports whether this process runs in a CI job, where secrets arrive
// from the workflow (a fetch step's outputs passed to the step) rather than a
// CLI sign-in. A variable so tests can pin it.
var InCI = func() bool {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CI"))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}
