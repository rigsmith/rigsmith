package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// A Desktop profile directory holds that account's entire logged-in session —
// cookies, token cache, the lot. Sync does reach inside a profile, but only
// through the same include-only allowlist it applies to the machine-wide install
// (engine/profiles.go), which reaches named session and config files and nothing
// else.
//
// What must never happen is the store falling INSIDE a configured root, because
// then the root's own walk would sweep the whole tree — every profile, every
// file, credentials included — with no allowlist standing between it and the
// remote. The store therefore lives under ~/.clauderig, outside every root, and
// this test is the tripwire for anyone who later moves either one.
func TestDesktopProfilesLiveOutsideEverySyncRoot(t *testing.T) {
	st, err := desktopStore()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(st.Root)

	m := config.Detect("test")
	for _, r := range config.DefaultRoots() {
		loc := m.Resolver().Resolve(r.Location.RawFor(m.OS))
		syncRoot := filepath.Clean(loc.Path)
		if syncRoot == "." || syncRoot == "" {
			continue
		}
		if root == syncRoot || strings.HasPrefix(root+string(filepath.Separator), syncRoot+string(filepath.Separator)) {
			t.Fatalf("Desktop profile store %q is inside sync root %q (%s) — sync would push live sessions",
				root, syncRoot, r.ID)
		}
	}
}

func TestDesktopStoreIsUnderClauderig(t *testing.T) {
	st, err := desktopStore()
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(st.Root); base != "desktop" {
		t.Fatalf("profile store base = %q, want \"desktop\"", base)
	}
	if parent := filepath.Base(filepath.Dir(st.Root)); parent != ".clauderig" {
		t.Fatalf("profile store parent = %q, want \".clauderig\"", parent)
	}
}

// Sync derives a profile's location from a $HOME-relative template so it
// resolves on a machine that has never run `clauderig desktop` — which means the
// engine and the profile store compute the same path by two different routes. If
// either moves, sync would walk a directory nothing writes to and report a clean
// backup of nothing.
func TestSyncResolvesProfilesWhereTheStoreKeepsThem(t *testing.T) {
	st, err := desktopStore()
	if err != nil {
		t.Fatal(err)
	}
	m := config.Detect("test")
	if m.Home == "" {
		t.Skip("no home directory on this machine")
	}
	got, status := engine.ProfileDir("work", m)
	if status != pathmap.StatusResolved {
		t.Fatalf("ProfileDir status = %v, want resolved", status)
	}
	want := filepath.Join(st.Root, "work")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("sync would read %q, but the store writes %q", got, want)
	}
}
