package planner

import (
	"strings"
	"testing"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/plugin"
)

func idCS(id string, releases ...changeset.Release) *changeset.Changeset {
	return &changeset.Changeset{ID: id, Summary: "a change", Releases: releases}
}

// A changeset whose packages all release is consumed; one naming only ignored
// packages is kept for a future run (Node-verified: @changesets leaves it on
// disk and exits 0).
func TestPartitionChangesetsKeepsIgnoredOnly(t *testing.T) {
	pkgs := []plugin.Package{pkg("A", "1.0.0"), pkg("B", "1.0.0")}
	cfg := config.Default()
	cfg.Ignore = []string{"B"}
	a := idCS("a", changeset.Release{Name: "A", Bump: changeset.BumpPatch})
	b := idCS("b", changeset.Release{Name: "B", Bump: changeset.BumpPatch})

	consumed, kept, err := PartitionChangesets([]*changeset.Changeset{a, b}, pkgs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumed) != 1 || consumed[0].ID != "a" {
		t.Errorf("consumed = %v, want [a]", ids(consumed))
	}
	if len(kept) != 1 || kept[0].ID != "b" {
		t.Errorf("kept = %v, want [b]", ids(kept))
	}
}

// A changeset mixing ignored and not-ignored packages is a hard error
// (Node-verified: "Mixed changesets that contain both ignored and not ignored
// packages are not allowed").
func TestPartitionChangesetsMixedIgnoredIsError(t *testing.T) {
	pkgs := []plugin.Package{pkg("A", "1.0.0"), pkg("B", "1.0.0")}
	cfg := config.Default()
	cfg.Ignore = []string{"B"}
	mixed := idCS("ab",
		changeset.Release{Name: "A", Bump: changeset.BumpPatch},
		changeset.Release{Name: "B", Bump: changeset.BumpPatch})

	_, _, err := PartitionChangesets([]*changeset.Changeset{mixed}, pkgs, cfg)
	if err == nil || !strings.Contains(err.Error(), "mixes ignored") {
		t.Fatalf("err = %v, want mixed-changeset error", err)
	}
}

// A changeset naming a package that isn't in the workspace is a hard error
// (Node-verified: nothing is versioned or removed).
func TestPartitionChangesetsUnknownPackageIsError(t *testing.T) {
	pkgs := []plugin.Package{pkg("A", "1.0.0")}
	ghost := idCS("c", changeset.Release{Name: "Ghost", Bump: changeset.BumpPatch})

	_, _, err := PartitionChangesets([]*changeset.Changeset{ghost}, pkgs, config.Default())
	if err == nil || !strings.Contains(err.Error(), "not in the workspace") {
		t.Fatalf("err = %v, want unknown-package error", err)
	}
}

// An empty changeset (no releases, `add --empty`) is consumed like any other.
func TestPartitionChangesetsEmptyIsConsumed(t *testing.T) {
	consumed, kept, err := PartitionChangesets([]*changeset.Changeset{idCS("empty")}, nil, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(consumed) != 1 || len(kept) != 0 {
		t.Errorf("consumed/kept = %v/%v, want [empty]/[]", ids(consumed), ids(kept))
	}
}

func ids(changesets []*changeset.Changeset) []string {
	out := make([]string, 0, len(changesets))
	for _, cs := range changesets {
		out = append(out, cs.ID)
	}
	return out
}
