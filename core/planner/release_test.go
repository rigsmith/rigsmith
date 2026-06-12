package planner

import (
	"fmt"
	"testing"
	"time"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/semver"
)

func mod(current string, bump changeset.Bump) *Module {
	return &Module{Name: "X", Current: semver.MustParse(current), cascadeBump: bump}
}

func TestPreVersion(t *testing.T) {
	cases := []struct {
		current string
		bump    changeset.Bump
		tag     string
		want    string
	}{
		{"1.2.0", changeset.BumpMinor, "next", "1.3.0-next.0"},
		{"1.3.0-next.0", changeset.BumpPatch, "next", "1.3.0-next.1"},
		{"1.3.0-next.1", changeset.BumpMajor, "next", "2.0.0-next.2"},
		{"1.0.0", changeset.BumpPatch, "rc", "1.0.1-rc.0"},
	}
	for _, c := range cases {
		got := PreVersion(mod(c.current, c.bump), c.tag)
		if got != c.want {
			t.Errorf("PreVersion(%s,%v,%s) = %s, want %s", c.current, c.bump, c.tag, got, c.want)
		}
	}
}

func TestSnapshotVersion(t *testing.T) {
	m := mod("1.2.0", changeset.BumpMinor)
	if got := SnapshotVersion(m, false, "canary-123"); got != "0.0.0-canary-123" {
		t.Errorf("SnapshotVersion default base = %s", got)
	}
	if got := SnapshotVersion(m, true, "canary-123"); got != "1.3.0-canary-123" {
		t.Errorf("SnapshotVersion calculated base = %s", got)
	}
}

func TestSnapshotSuffix(t *testing.T) {
	ref := time.Date(2026, 6, 11, 19, 10, 55, 0, time.UTC)
	dt := "20260611191055"

	// No template: join non-empty parts.
	if got, _ := SnapshotSuffix("", "", "", ref); got != dt {
		t.Errorf("no template, no tag = %q, want %q", got, dt)
	}
	if got, _ := SnapshotSuffix("", "canary", "", ref); got != "canary-"+dt {
		t.Errorf("no template, tag = %q", got)
	}
	// Template with placeholders.
	if got, _ := SnapshotSuffix("{tag}.{commit}", "rc", "abc1234", ref); got != "rc.abc1234" {
		t.Errorf("template = %q", got)
	}
	// Every placeholder fills ({timestamp} is unix millis, {datetime} the 14-digit form).
	ts := fmt.Sprintf("%d", ref.UnixMilli())
	if got, _ := SnapshotSuffix("{tag}-{datetime}-{timestamp}-{commit}", "rc", "abc1234", ref); got != "rc-"+dt+"-"+ts+"-abc1234" {
		t.Errorf("all placeholders = %q", got)
	}
	// {tag} missing but tag set → error.
	if _, err := SnapshotSuffix("{datetime}", "rc", "", ref); err == nil {
		t.Error("expected error when {tag} missing but tag set")
	}
	// A non-tag placeholder with no value → error ({tag} alone may be empty).
	if _, err := SnapshotSuffix("{commit}", "", "", ref); err == nil {
		t.Error("expected error when {commit} is used but no commit is defined")
	}
	if got, err := SnapshotSuffix("{tag}", "", "", ref); err != nil || got != "" {
		t.Errorf("{tag} with empty tag = %q, %v; want empty, nil (tag may be blank)", got, err)
	}
}

func TestNormalModeResolvesStableBump(t *testing.T) {
	// A plain plan (no ApplyPre/ApplySnapshot) resolves to the stable bump with
	// no version override.
	pkgs := []plugin.Package{pkg("Core", "1.0.0")}
	changesets := []*changeset.Changeset{cs("a change", changeset.Release{Name: "Core", Bump: changeset.BumpMinor})}
	plan := Plan(changesets, pkgs, config.Default())

	if len(plan) != 1 {
		t.Fatalf("plan should hold one module, got %d", len(plan))
	}
	m := plan[0]
	if m.VersionOverride != "" {
		t.Errorf("VersionOverride = %q, want empty in normal mode", m.VersionOverride)
	}
	if got := m.ResolvedVersion(); got != "1.1.0" {
		t.Errorf("ResolvedVersion = %s, want 1.1.0", got)
	}
}

func TestFixedGroupInPrereleaseCoordinatesCounter(t *testing.T) {
	// A is further along in pre mode (next.2) than B (next.0). The fixed group
	// coordinates both onto the highest current version, so PreVersion derives
	// the same (highest) counter for both — this is how fixed/linked groups
	// share their prerelease counter.
	pkgs := []plugin.Package{pkg("A", "1.1.0-next.2"), pkg("B", "1.1.0-next.0")}
	cfg := config.Default()
	cfg.Fixed = [][]string{{"A", "B"}}
	changesets := []*changeset.Changeset{cs("a fix", changeset.Release{Name: "A", Bump: changeset.BumpPatch})}
	plan := Plan(changesets, pkgs, cfg)

	for _, name := range []string{"A", "B"} {
		m := findModule(plan, name)
		if m == nil {
			t.Fatalf("%s missing from plan", name)
		}
		if got := m.Current.String(); got != "1.1.0-next.2" {
			t.Errorf("%s Current = %s, want 1.1.0-next.2 (coordinated to highest)", name, got)
		}
		if got := PreVersion(m, "next"); got != "1.1.0-next.3" {
			t.Errorf("%s PreVersion = %s, want 1.1.0-next.3", name, got)
		}
	}
}

func TestGraduatePrereleases(t *testing.T) {
	// A prerelease package with no changeset graduates; a stable one does not.
	pkgs := []plugin.Package{
		{Name: "Pre", Version: "1.1.0-next.3", ManifestPath: "Pre.csproj"},
		{Name: "Stable", Version: "2.0.0", ManifestPath: "Stable.csproj"},
	}
	plan := GraduatePrereleases(nil, pkgs)
	if len(plan) != 1 || plan[0].Name != "Pre" {
		t.Fatalf("expected only Pre to graduate, got %+v", plan)
	}
	if got := plan[0].NewVersion().String(); got != "1.1.0" {
		t.Errorf("graduated version = %s, want 1.1.0", got)
	}
}
