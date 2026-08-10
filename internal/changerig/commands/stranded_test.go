package commands

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

func testPkgs() []plugin.Package {
	return []plugin.Package{
		{Name: "github.com/rigsmith/rigsmith", Version: "1.5.0"},
		{Name: "demo-app", Version: "0.1.0"},
	}
}

func testCfg(t *testing.T, src string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestFindStrandedNamesTheRealCause covers the trap this exists for: front
// matter with a type but no package line parses cleanly, counts as a pending
// changeset, and releases nothing — forever.
func TestFindStrandedNamesTheRealCause(t *testing.T) {
	cfg := testCfg(t, `{ "ignore": ["demo-app"] }`)
	css := []*changeset.Changeset{
		{ID: "no-package", Type: "fix"}, // the trap
		{ID: "typo", Releases: []changeset.Release{{Name: "github.com/rigsmith/rigsmit"}}},                       // misspelled
		{ID: "ignored-only", Releases: []changeset.Release{{Name: "demo-app"}}},                                  // never releases
		{ID: "fine", Releases: []changeset.Release{{Name: "github.com/rigsmith/rigsmith"}}},                      // healthy
		{ID: "mixed", Releases: []changeset.Release{{Name: "demo-app"}, {Name: "github.com/rigsmith/rigsmith"}}}, // one live target is enough
	}

	got := FindStranded(css, testPkgs(), cfg)

	byID := map[string]string{}
	for _, s := range got {
		byID[s.ID] = s.Reason
	}
	if len(got) != 3 {
		t.Fatalf("stranded = %v, want exactly the three that can never release", byID)
	}
	if r := byID["no-package"]; r != "names no package" {
		t.Errorf("no-package reason = %q", r)
	}
	if r := byID["typo"]; !strings.Contains(r, "unknown") || !strings.Contains(r, "rigsmit") {
		t.Errorf("typo reason = %q, want it to name the unknown package", r)
	}
	if r := byID["ignored-only"]; !strings.Contains(r, "ignored") {
		t.Errorf("ignored-only reason = %q", r)
	}
	if _, ok := byID["fine"]; ok {
		t.Error("a changeset naming a releasable package is not stranded")
	}
	if _, ok := byID["mixed"]; ok {
		t.Error("one releasable target is enough — not stranded")
	}
}

func TestFindStrandedIsSortedAndEmptyWhenHealthy(t *testing.T) {
	pkgs := testPkgs()
	cfg := testCfg(t, `{}`)

	if got := FindStranded(nil, pkgs, cfg); len(got) != 0 {
		t.Errorf("no changesets = %v, want none stranded", got)
	}
	healthy := []*changeset.Changeset{{ID: "a", Releases: []changeset.Release{{Name: "demo-app"}}}}
	if got := FindStranded(healthy, pkgs, cfg); len(got) != 0 {
		t.Errorf("healthy changeset reported as stranded: %v", got)
	}

	css := []*changeset.Changeset{{ID: "zulu"}, {ID: "alpha"}, {ID: "mike"}}
	got := FindStranded(css, pkgs, cfg)
	if len(got) != 3 || got[0].ID != "alpha" || got[1].ID != "mike" || got[2].ID != "zulu" {
		t.Errorf("stranded order = %v, want sorted by id for a stable report", got)
	}
}

// TestStrandedHintNamesThePackageWhenUnambiguous: with one releasable package
// the fix is exact, so spell it out rather than making the reader look it up.
func TestStrandedHintNamesThePackageWhenUnambiguous(t *testing.T) {
	single := []plugin.Package{{Name: "github.com/rigsmith/rigsmith"}, {Name: "demo-app"}}
	cfg := testCfg(t, `{ "ignore": ["demo-app"] }`)
	hint := StrandedHint("changerig", single, cfg)
	if !strings.Contains(hint, "-p github.com/rigsmith/rigsmith") {
		t.Errorf("hint = %q, want the ready-to-run command", hint)
	}

	multi := testPkgs()
	if h := StrandedHint("shiprig", multi, testCfg(t, `{}`)); strings.Contains(h, "-p ") {
		t.Errorf("hint = %q, want no package guess when several are releasable", h)
	}
}
