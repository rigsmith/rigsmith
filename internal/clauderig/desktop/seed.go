package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// Seeding a new profile from the existing Claude Desktop install.
//
// A fresh `--user-data-dir` is genuinely empty: no MCP servers, no theme, no
// preferences. That is correct for the LOGIN — the entire point of the
// per-profile model is that a profile's credentials are its own — but it is
// needlessly unhelpful for everything else, since the settings are the user's
// and identical whichever account they are signed into.
//
// So a new profile is seeded from the default install's configuration, and only
// from the parts that carry no identity.

// seedFiles are copied verbatim into a new profile. Each is small, declarative
// configuration that belongs to the person rather than to the account.
//
// The list is deliberately short, and deliberately does NOT contain the app's
// state directories — Cookies, Local Storage, Session Storage — which hold the
// claude.ai session, nor `config.json`, which is filtered rather than copied
// (see SeedConfigJSON).
var seedFiles = []string{
	"claude_desktop_config.json", // MCP servers: the thing users actually miss
	"extensions-blocklist.json",
	"git-worktrees.json",
	"cowork-enabled-cli-ops.json",
}

// SeedResult reports what seeding copied, for the caller to narrate.
type SeedResult struct {
	Files  []string // configuration files copied verbatim
	Config bool     // config.json preferences were carried over
}

// Empty reports that nothing was seeded (no source install, or nothing to take).
func (s SeedResult) Empty() bool { return len(s.Files) == 0 && !s.Config }

// Seed copies portable configuration from sourceRoot into a new profile.
//
// Never copies anything that would carry the login: the credential-bearing keys
// of config.json are dropped by construction (only config.DesktopConfigKeepKeys
// survive), and the session state directories are not in the list at all. A
// seeded profile still starts signed out, which is the whole point.
//
// Best-effort per file: a source that does not exist is simply skipped, because
// seeding is a convenience and must never be the reason `add` fails.
func Seed(p Profile, sourceRoot string) (SeedResult, error) {
	var res SeedResult
	if sourceRoot == "" {
		return res, nil
	}
	// Never seed a profile from itself — a no-op at best, and at worst it would
	// truncate the live install's own config through the filter below.
	if sameDir(sourceRoot, p.DataDir()) {
		return res, nil
	}
	for _, name := range seedFiles {
		src := filepath.Join(sourceRoot, name)
		if _, err := os.Stat(src); err != nil {
			continue // not present in this install
		}
		if err := copyFile(src, filepath.Join(p.DataDir(), name), 0o600); err != nil {
			return res, fmt.Errorf("seed %s: %w", name, err)
		}
		res.Files = append(res.Files, name)
	}
	ok, err := seedConfigJSON(sourceRoot, p.DataDir())
	if err != nil {
		return res, err
	}
	res.Config = ok
	return res, nil
}

// seedConfigJSON copies ONLY the portable keys of Desktop's config.json.
//
// The file mixes preferences with the login: `oauth:tokenCache`,
// `oauth:tokenCacheV2` and `lastKnownAccountUuid` sit beside `locale` and
// `userThemeMode`. Copying it wholesale would hand the new profile the old
// profile's session — precisely the thing the per-profile model exists to avoid,
// and precisely what the withdrawn session-switching feature did. So the
// document is rebuilt from the allowed keys rather than filtered in place, which
// makes the safe set additive: a key nobody has vetted is absent by default.
func seedConfigJSON(sourceRoot, dataDir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(sourceRoot, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, nil // unreadable source: skip rather than fail the add
	}
	var all map[string]json.RawMessage
	if uerr := json.Unmarshal(raw, &all); uerr != nil {
		return false, nil // not an object we understand
	}
	kept := map[string]json.RawMessage{}
	for _, k := range config.DesktopConfigKeepKeys() {
		if v, ok := all[k]; ok {
			kept[k] = v
		}
	}
	if len(kept) == 0 {
		return false, nil
	}
	body, merr := json.MarshalIndent(kept, "", "  ")
	if merr != nil {
		return false, merr
	}
	dst := filepath.Join(dataDir, "config.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, append(body, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// SeedSources lists what Seed would copy, for help text.
func SeedSources() []string { return append([]string{"config.json (preferences only)"}, seedFiles...) }
