package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

func TestLoad_MissingVsCorrupt(t *testing.T) {
	dir := t.TempDir()
	// absent → an IsNotExist error (so LoadOrDefault falls back to Default)
	if _, err := Load(dir); !os.IsNotExist(err) {
		t.Errorf("missing config should be IsNotExist, got %v", err)
	}
	// present but corrupt → a non-IsNotExist error (so LoadOrDefault surfaces it
	// instead of silently replacing the user's config with defaults)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{ corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, e := Load(dir); e == nil || os.IsNotExist(e) {
		t.Errorf("corrupt config should surface a non-IsNotExist error, got %v", e)
	}
}

func TestLoad_MaxFileBytesDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	// A config written before the cap existed: absent must mean the default, since
	// long-lived configs are exactly the ones carrying an oversized transcript.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"schema":1,"retention":{"historyDays":30}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention.MaxFileBytes != DefaultMaxFileBytes {
		t.Errorf("MaxFileBytes = %d, want default %d", c.Retention.MaxFileBytes, DefaultMaxFileBytes)
	}
	// Disabling stays possible, but only explicitly.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"schema":1,"retention":{"maxFileBytes":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention.MaxFileBytes != -1 {
		t.Errorf("explicit -1 should disable the cap, got %d", c.Retention.MaxFileBytes)
	}
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.Retention.HistoryDays != 90 || c.Retention.SquashFactor != 2.0 || c.Retention.FloorBytes != 500<<20 {
		t.Errorf("retention defaults wrong: %+v", c.Retention)
	}
	if len(c.Roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(c.Roots))
	}
}

func TestRootLocation_PerOS(t *testing.T) {
	c := Default()
	mac := Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	win := Machine{Name: "pc", OS: pathmap.OSWindows, Home: `C:\Users\John`}
	lin := Machine{Name: "box", OS: pathmap.OSLinux, Home: "/home/john"}

	// CLI root identical everywhere
	if got, st := c.RootLocation("cli", mac); st != pathmap.StatusResolved || got != "/Users/john/.claude" {
		t.Errorf("cli mac = %q (%v)", got, st)
	}
	if got, _ := c.RootLocation("cli", win); got != `C:\Users\John\.claude` {
		t.Errorf("cli win = %q", got)
	}

	// Desktop root differs per OS
	if got, _ := c.RootLocation("desktop", mac); got != "/Users/john/Library/Application Support/Claude" {
		t.Errorf("desktop mac = %q", got)
	}
	if got, _ := c.RootLocation("desktop", win); got != `C:\Users\John\AppData\Roaming\Claude` {
		t.Errorf("desktop win = %q", got)
	}
	if got, _ := c.RootLocation("desktop", lin); got != "/home/john/.config/Claude" {
		t.Errorf("desktop linux = %q", got)
	}
}

func TestRootLocation_Unknown(t *testing.T) {
	if _, st := Default().RootLocation("nope", Machine{OS: pathmap.OSMacOS, Home: "/x"}); st != pathmap.StatusInvalid {
		t.Error("unknown root should be invalid")
	}
}

func TestMachineResolverAndFolders(t *testing.T) {
	m := Machine{OS: pathmap.OSMacOS, Home: "/Users/john", Tokens: map[string]string{"DROPBOX": "/Users/john/Dropbox"}}
	if got := m.Resolver().Resolve("$DROPBOX/x"); got.Path != "/Users/john/Dropbox/x" {
		t.Errorf("custom token resolve = %+v", got)
	}
	if m.Folders()["HOME"] != "/Users/john" {
		t.Error("HOME missing from folders")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.Remote = "git@github.com:john/claude-sync.git"
	c.Machines["mbp"] = Detect("mbp")
	if err := Save(c, dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Remote != c.Remote || len(got.Machines) != 1 || len(got.Roots) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// the per-OS desktop cascade survives the round-trip
	if got.Roots[1].Location.PerOS[pathmap.OSWindows] != `$HOME/AppData/Roaming/Claude` {
		t.Errorf("desktop cascade lost: %+v", got.Roots[1].Location)
	}

	// Save now emits a schema-stamped JSONC document (leading comment + $schema),
	// and the comment round-trips through the JSONC loader.
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.HasPrefix(body, "//") {
		t.Errorf("config.json should open with a // JSONC header:\n%s", body)
	}
	if !strings.Contains(body, `"$schema": "`+SchemaURL+`"`) {
		t.Errorf("config.json missing the $schema stamp:\n%s", body)
	}
}

// The keep-list is shared by sync (which prunes the synced copy) and by profile
// seeding (which rebuilds a new profile's config from it). It is the single
// place deciding what is safe to copy out of Desktop's config.json, so the
// credential-bearing keys must never appear in it.
func TestDesktopConfigKeepKeysExcludeTheLogin(t *testing.T) {
	for _, k := range DesktopConfigKeepKeys() {
		switch k {
		case "oauth:tokenCache", "oauth:tokenCacheV2", "lastKnownAccountUuid":
			t.Fatalf("%q is credential/identity-bearing and must never be copied", k)
		}
	}
	want := map[string]bool{"preferences": true, "locale": true, "userThemeMode": true}
	for _, k := range DesktopConfigKeepKeys() {
		if !want[k] {
			t.Errorf("unvetted key %q in the keep-list — additions here widen what sync and seeding copy", k)
		}
	}
}

func TestLoad_LargeFileBytesDefaultsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	// A config written before the throttle existed: absent means the default,
	// since long-lived configs are the ones with long sessions in them.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"schema":1,"retention":{"historyDays":30}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention.LargeFileBytes != DefaultLargeFileBytes {
		t.Fatalf("LargeFileBytes = %d, want default %d", c.Retention.LargeFileBytes, DefaultLargeFileBytes)
	}
}

func TestLoad_LargeFileBytesNegativeDisables(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"schema":1,"retention":{"largeFileBytes":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Retention.LargeFileBytes != -1 {
		t.Fatalf("LargeFileBytes = %d, want -1 kept as an explicit off", c.Retention.LargeFileBytes)
	}
}

// DetectFor is what identifies "this machine" to both the CLI and the UI. It
// must always come back named: the UI's device board marks the current row by
// comparing this name against the registry, so an empty one silently makes
// every machine look like someone else's.
func TestDetectFor_AlwaysNamed(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Machines: map[string]Machine{
		"registered": {Name: "registered", OS: OSToken(), Home: home},
	}}

	me := DetectFor(cfg)
	if me.Name != "registered" {
		t.Errorf("Name = %q, want the matching config entry", me.Name)
	}
	if me.OS != OSToken() || me.Home != home {
		t.Errorf("DetectFor lost the host identity: %+v", me)
	}

	// Even with nothing registered it must not return an empty name.
	if me := DetectFor(&Config{}); me.Name == "" {
		t.Error("DetectFor returned an unnamed machine for an empty config")
	}
}

// Identity resolution has two jobs that used to be one. Display paths always
// need a name, so ResolveName still falls back to a placeholder — but anything
// that WRITES an identity has to know the difference, or it registers a ghost
// into the synced registry that every other machine then inherits.
func TestIdentityResolved(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	// A matching config entry is a real identity.
	matched := &Config{Machines: map[string]Machine{
		"registered": {Name: "registered", OS: OSToken(), Home: home},
	}}
	if !IdentityResolved(matched) {
		t.Error("a matching config entry should count as resolved")
	}
	if got := ResolveName(matched); got != "registered" {
		t.Errorf("ResolveName = %q, want the config entry", got)
	}

	// With nothing registered we fall back to the hostname, which on any real
	// machine still counts as resolved — the placeholder is the last resort.
	empty := &Config{}
	name := ResolveName(empty)
	if name == "" {
		t.Fatal("ResolveName returned an empty name")
	}
	if IdentityResolved(empty) == (name == UnresolvedName) {
		t.Errorf("IdentityResolved disagrees with the name it produced (%q)", name)
	}
}

// The placeholder must stay exactly the ghost's name, so an existing "this"
// entry in a synced registry is still recognisable as the same bug.
func TestUnresolvedNameIsTheHistoricalGhost(t *testing.T) {
	if UnresolvedName != "this" {
		t.Fatalf("UnresolvedName = %q; the June 2026 ghost was named %q", UnresolvedName, "this")
	}
}
