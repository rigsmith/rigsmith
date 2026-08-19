package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realConfigJSON mirrors the shape of a real Desktop config.json: portable
// preferences sitting beside the login and a pile of caches.
const realConfigJSON = `{
  "locale": "en-US",
  "userThemeMode": "dark",
  "oauth:tokenCache": "SECRET-TOKEN-CACHE",
  "oauth:tokenCacheV2": "SECRET-TOKEN-CACHE-V2",
  "lastKnownAccountUuid": "456fc32e-7579-49c7-bb2a-099657892c6a",
  "dxt:allowlistCache:f1eab509": "cache",
  "updaterLastSeenVersion": "1.32885.1",
  "first_launch_at": "2026-01-01"
}`

func seedSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "config.json"), realConfigJSON)
	writeFile(t, filepath.Join(src, "claude_desktop_config.json"), `{"mcpServers":{"x":{"command":"y"}}}`)
	writeFile(t, filepath.Join(src, "git-worktrees.json"), `{}`)
	// State that must never be seeded — this is the login.
	writeFile(t, filepath.Join(src, "Cookies"), "sqlite-cookie-db")
	writeFile(t, filepath.Join(src, "Local Storage", "leveldb", "000003.log"), "session state")
	return src
}

// The one property that must never regress: seeding hands over settings, never
// the login. Copying config.json wholesale would give a new profile the old
// profile's session — exactly what the withdrawn session-switching feature did.
func TestSeedNeverCopiesTheLogin(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(p, seedSource(t)); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(p.DataDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, forbidden := range []string{
		"oauth:tokenCache", "oauth:tokenCacheV2", "SECRET-TOKEN-CACHE",
		"lastKnownAccountUuid", "456fc32e", "dxt:allowlistCache", "updaterLastSeenVersion", "first_launch_at",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("seeded config.json contains %q — the login or machine state leaked into the new profile", forbidden)
		}
	}
	var kept map[string]any
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}
	if kept["locale"] != "en-US" || kept["userThemeMode"] != "dark" {
		t.Fatalf("portable preferences were not carried over: %v", kept)
	}

	// The session state directories are not copied at all.
	for _, never := range []string{"Cookies", "Local Storage"} {
		if _, serr := os.Stat(filepath.Join(p.DataDir(), never)); !os.IsNotExist(serr) {
			t.Errorf("%s was seeded into the new profile — that is the claude.ai session", never)
		}
	}
}

func TestSeedCopiesTheConfigUsersActuallyMiss(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Seed(p, seedSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Config {
		t.Error("preferences were not seeded")
	}
	got := readFile(t, filepath.Join(p.DataDir(), "claude_desktop_config.json"))
	if !strings.Contains(got, "mcpServers") {
		t.Fatalf("MCP servers were not seeded: %q", got)
	}
	if len(res.Files) < 2 {
		t.Fatalf("Files = %v, want the MCP config and the worktrees file", res.Files)
	}
}

// Seeding is a convenience: a source that isn't there must not fail `add`.
func TestSeedIsQuietWithoutASource(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Seed(p, filepath.Join(t.TempDir(), "no-such-install"))
	if err != nil {
		t.Fatalf("Seed failed on a missing source: %v", err)
	}
	if !res.Empty() {
		t.Fatalf("res = %+v, want nothing seeded", res)
	}
	if _, serr := Seed(p, ""); serr != nil {
		t.Fatalf("Seed failed on an empty source: %v", serr)
	}
}

// Seeding a profile from itself would rewrite the live install's config through
// the keep-filter — deleting the user's login from their own Desktop.
func TestSeedRefusesToSeedFromItself(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.DataDir(), "config.json"), realConfigJSON)
	res, err := Seed(p, p.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Empty() {
		t.Fatalf("res = %+v, want a no-op", res)
	}
	if got := readFile(t, filepath.Join(p.DataDir(), "config.json")); !strings.Contains(got, "oauth:tokenCache") {
		t.Fatal("seeding from itself rewrote the profile's own config, dropping its login")
	}
}
