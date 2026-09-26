package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The @changesets v3 prerelease layout: consumed changesets wait in
// .changeset/pre/ until the run after `pre exit` graduates them. These cover
// the paths around that wait that the parity lifecycle does not walk.

// prereleaseThenExit leaves dir in exit mode with cs1 (a pkg-a minor) waiting
// in .changeset/pre/ and nothing at the top level.
func prereleaseThenExit(t *testing.T, dir string) {
	t.Helper()
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a feature")
	for _, args := range [][]string{{"pre", "enter", "next"}, {"version"}, {"pre", "exit"}} {
		code, out := runChangerig(t, dir, args...)
		assertExitZero(t, code, out)
	}
	if !fileExists(filepath.Join(dir, ".changeset", "pre", "cs1.md")) {
		t.Fatal("precondition: cs1 should be waiting in .changeset/pre/")
	}
	if fileExists(filepath.Join(dir, ".changeset", "cs1.md")) {
		t.Fatal("precondition: nothing should be left at the top level")
	}
}

// status must count the graduating changesets, not stop at "no changesets".
func TestStatusReportsPrereleaseGraduation(t *testing.T) {
	dir := newWorkspace(t)
	prereleaseThenExit(t, dir)

	planPath := filepath.Join(dir, "plan.json")
	code, out := runChangerig(t, dir, "status", "--output", planPath)
	assertExitZero(t, code, out)

	var plan struct {
		Changesets []struct {
			ID string `json:"id"`
		} `json:"changesets"`
		Releases []struct {
			Name       string `json:"name"`
			NewVersion string `json:"newVersion"`
		} `json:"releases"`
		PreState *struct {
			Mode string `json:"mode"`
			Tag  string `json:"tag"`
		} `json:"preState"`
	}
	if err := json.Unmarshal([]byte(readFile(t, planPath)), &plan); err != nil {
		t.Fatalf("parse plan.json: %v", err)
	}
	if len(plan.Releases) != 1 || plan.Releases[0].Name != "pkg-a" || plan.Releases[0].NewVersion != "1.1.0" {
		t.Errorf("status should plan the graduation of pkg-a to 1.1.0, got %+v", plan.Releases)
	}
	// The graduating changeset is listed, and the plan carries the prerelease
	// state as @changesets' does.
	if len(plan.Changesets) != 1 || plan.Changesets[0].ID != "cs1" {
		t.Errorf("changesets = %+v, want the graduating cs1", plan.Changesets)
	}
	if plan.PreState == nil || plan.PreState.Mode != "exit" || plan.PreState.Tag != "next" {
		t.Errorf("preState = %+v, want mode exit, tag next", plan.PreState)
	}
}

// A snapshot is throwaway: it must not consume the changesets the stable
// graduation still needs.
func TestSnapshotAfterPreExitKeepsGraduatingChangesets(t *testing.T) {
	dir := newWorkspace(t)
	prereleaseThenExit(t, dir)
	writeChangeset(t, dir, "snap", "pkg-a", "patch", "a snapshot fix")

	code, out := runChangerig(t, dir, "version", "--snapshot", "canary")
	assertExitZero(t, code, out)

	if !fileExists(filepath.Join(dir, ".changeset", "pre", "cs1.md")) {
		t.Error("the snapshot consumed a graduating changeset from .changeset/pre/")
	}
	if got := readPreState(t, dir).Mode; got != "exit" {
		t.Errorf("pre.json mode = %q after a snapshot, want \"exit\"", got)
	}
}

// A prerelease begun under v2 lists its consumed changesets in pre.json. A
// run with nothing new still migrates them, even though it exits 1.
func TestVersionMigratesV2PrereleaseWithNothingNew(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "old", "pkg-a", "minor", "consumed under v2")
	writeFile(t, filepath.Join(dir, ".changeset", "pre.json"),
		`{ "mode": "pre", "tag": "next", "initialVersions": { "pkg-a": "1.0.0" }, "changesets": ["old"] }`)

	code, out := runChangerig(t, dir, "version")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "no unreleased changesets found")
	if !fileExists(filepath.Join(dir, ".changeset", "pre", "old.md")) {
		t.Error("old.md should have moved into .changeset/pre/")
	}
	if fileExists(filepath.Join(dir, ".changeset", "old.md")) {
		t.Error("old.md should no longer be at the top level")
	}
	if raw := readFile(t, filepath.Join(dir, ".changeset", "pre.json")); strings.Contains(raw, "changesets") {
		t.Errorf("pre.json should drop the v2 list once migrated:\n%s", raw)
	}
}

// A graduating changeset the exit run does not consume (its package is now
// ignored) goes back to the top level, where later runs will find it.
func TestPreExitReturnsUnconsumedGraduatingChangeset(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	initChangesets(t, dir)
	writeChangeset(t, dir, "cs-a", "pkg-a", "minor", "a feature")
	writeChangeset(t, dir, "cs-b", "pkg-b", "minor", "b feature")
	for _, args := range [][]string{{"pre", "enter", "next"}, {"version"}, {"pre", "exit"}} {
		code, out := runChangerig(t, dir, args...)
		assertExitZero(t, code, out)
	}
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-b"] }`)

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	if !fileExists(filepath.Join(dir, ".changeset", "cs-b.md")) {
		t.Error("cs-b should be back at the top level after the exit run")
	}
	for _, gone := range []string{"cs-a.md", "pre.json", "pre"} {
		if _, err := os.Stat(filepath.Join(dir, ".changeset", gone)); err == nil {
			t.Errorf(".changeset/%s should be gone after graduation", gone)
		}
	}
}
