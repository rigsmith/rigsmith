package planner

import (
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
)

func TestReleaseGroups(t *testing.T) {
	plan := []*Module{
		{Name: "props-a", VersionFile: "Directory.Build.props"},
		{Name: "props-b", VersionFile: "Directory.Build.props"},
		{Name: "linked-x"},
		{Name: "linked-y"},
		{Name: "shared-1"},
		{Name: "shared-2"},
		{Name: "dep"},
		{Name: "user"},
		{Name: "alone"},
	}
	byName := map[string]*Module{}
	for _, m := range plan {
		byName[m.Name] = m
	}
	byName["user"].depLinks = []depLink{{dep: byName["dep"], rng: "^1.0.0"}}
	cfg := config.Default()
	cfg.Linked = [][]string{{"linked-y", "linked-x", "not-in-plan"}}
	changesets := []*changeset.Changeset{
		{ID: "both", Releases: []changeset.Release{{Name: "shared-2"}, {Name: "shared-1"}}},
		{ID: "one", Releases: []changeset.Release{{Name: "alone"}}},
	}

	got := ReleaseGroups(plan, changesets, cfg)
	want := map[string]string{
		"props-a":  "props-a", // a shared version file
		"props-b":  "props-a",
		"linked-x": "linked-x", // a linked group; a member outside the plan is ignored
		"linked-y": "linked-x",
		"shared-1": "shared-1", // one changeset names both
		"shared-2": "shared-1",
		"dep":      "dep", // a dependency link
		"user":     "dep",
		"alone":    "alone",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v\nwant     %v", got, want)
	}
}

// A fixed group (or changeset) whose first name isn't releasing still joins
// the members that are.
func TestReleaseGroupsAnchorOnAPlannedMember(t *testing.T) {
	plan := []*Module{{Name: "fx-b"}, {Name: "fx-c"}, {Name: "cs-b"}, {Name: "cs-c"}}
	cfg := config.Default()
	cfg.Fixed = [][]string{{"not-in-plan", "fx-c", "fx-b"}}
	changesets := []*changeset.Changeset{
		{ID: "cs", Releases: []changeset.Release{{Name: "gone"}, {Name: "cs-c"}, {Name: "cs-b"}}},
	}
	got := ReleaseGroups(plan, changesets, cfg)
	if got["fx-b"] != "fx-b" || got["fx-c"] != "fx-b" {
		t.Errorf("fixed group split: %v", got)
	}
	if got["cs-b"] != "cs-b" || got["cs-c"] != "cs-b" {
		t.Errorf("changeset group split: %v", got)
	}
}
