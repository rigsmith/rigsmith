package bridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

var epoch = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func testStatus() *Status {
	return &Status{now: func() time.Time { return epoch }}
}

func TestSnapshotMapsInfo(t *testing.T) {
	info := status.Info{
		Machine:    config.Machine{Name: "Johns-MacBook-Air13", OS: "macos"},
		Remote:     "https://github.com/example/claude.git",
		HasStaging: true,
		LastSync:   "3bf60f4 3 minutes ago — clauderig sync",
		Divergence: gitrepo.Divergence{Ref: "origin/main", Tracked: true, Ahead: 15, Behind: 65},
		Roots: []status.RootInfo{
			{ID: "cli", Files: 1382, Present: true},
			{ID: "desktop", Present: false},
		},
		Hooks: []string{"SessionStart", "Stop"},
		Devices: []devices.Device{
			{Name: "Johns-MacBook-Pro16", OS: "macos", LastSync: epoch.Add(-time.Hour), ClaudeVersion: "2.1.0"},
			{Name: "Johns-MacBook-Air13", OS: "macos", LastSync: epoch.Add(-2 * time.Minute)},
		},
	}

	snap := testStatus().snapshot(info, journal.Record{})

	if snap.Machine != "Johns-MacBook-Air13" || snap.OS != "macos" {
		t.Errorf("machine = %q/%q", snap.Machine, snap.OS)
	}
	if snap.Level != "red" || snap.Reason != "diverged" {
		t.Errorf("level/reason = %q/%q, want red/diverged", snap.Level, snap.Reason)
	}
	if snap.Ahead != 15 || snap.Behind != 65 {
		t.Errorf("ahead/behind = %d/%d", snap.Ahead, snap.Behind)
	}
	if snap.TakenAt != epoch {
		t.Errorf("TakenAt = %v, want %v", snap.TakenAt, epoch)
	}
	if len(snap.Roots) != 2 || snap.Roots[0].Files != 1382 || snap.Roots[1].Present {
		t.Errorf("roots = %+v", snap.Roots)
	}
}

// The board has to mark which row is this machine — the ghost-device incident
// started with a registry entry nobody could attribute.
func TestSnapshotFlagsThisMachine(t *testing.T) {
	info := status.Info{
		Machine:    config.Machine{Name: "Johns-MacBook-Air13"},
		HasStaging: true,
		Divergence: gitrepo.Divergence{Tracked: true},
		Devices: []devices.Device{
			{Name: "Johns-MacBook-Pro16"},
			{Name: "Johns-MacBook-Air13"},
		},
	}

	snap := testStatus().snapshot(info, journal.Record{})
	if snap.Devices[0].This {
		t.Error("Pro16 marked as this machine")
	}
	if !snap.Devices[1].This {
		t.Error("Air13 is this machine but was not marked")
	}
}

// Level and Reason are the frontend's whole contract; they must survive the
// round trip as the stable lowercase tokens the CSS and JS branch on.
func TestSnapshotJSONTokens(t *testing.T) {
	info := status.Info{HasStaging: true, Divergence: gitrepo.Divergence{Tracked: true}}
	b, err := json.Marshal(testStatus().snapshot(info, journal.Record{}))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["level"] != "green" || got["reason"] != "synced" {
		t.Fatalf("level/reason = %v/%v, want green/synced", got["level"], got["reason"])
	}
	for _, key := range []string{"machine", "level", "reason", "summary", "ahead", "behind", "devices", "roots"} {
		if _, ok := got[key]; !ok {
			t.Errorf("snapshot JSON missing %q", key)
		}
	}
}

func TestSettingsPathPrefersMachineHome(t *testing.T) {
	got, err := settingsPath(config.Machine{Home: "/tmp/fakehome"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/fakehome/.claude/settings.json"; got != want {
		t.Fatalf("settingsPath = %q, want %q", got, want)
	}
}
