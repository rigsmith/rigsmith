package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// A Desktop profile directory holds that account's entire logged-in session —
// cookies, token cache, the lot. If it ever fell inside a sync root, `clauderig
// sync` would push live credentials to the remote. The store therefore lives
// under ~/.clauderig, outside every root, and this test is the tripwire for
// anyone who later moves either one.
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
