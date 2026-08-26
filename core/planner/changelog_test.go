package planner

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// The scope becomes the bullet's lead-in and its sort key; the type decides the
// section. Together that is the whole point: features lead, each tool's lines
// sit together, and nobody has to name a file to get the order right.
func TestRenderGroupsByTypeAndLeadsWithScope(t *testing.T) {
	changes := []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Scope: "rig", Summary: "fix(rig): a rig fix"},
		{Bump: "minor", Type: "feat", Scope: "rig", Summary: "feat(rig): a rig feature"},
		{Bump: "minor", Type: "feat", Scope: "clauderig", Summary: "feat(clauderig): a clauderig feature"},
		{Bump: "patch", Type: "refactor", Summary: "refactor: an unscoped tidy"},
	}
	got := renderSections("1.1.0", changes, config.DefaultChangelogGroups)

	// Sections in group order: enhancements, then fixes, then refactors.
	wantOrder := []string{"🚀 Enhancements", "🩹 Fixes", "💅 Refactors"}
	at := -1
	for _, section := range wantOrder {
		i := strings.Index(got, "### "+section)
		if i < 0 {
			t.Fatalf("missing section %q in:\n%s", section, got)
		}
		if i < at {
			t.Errorf("section %q is out of order in:\n%s", section, got)
		}
		at = i
	}
	// Scope leads the bullet, and the prefix is gone from the prose.
	if !strings.Contains(got, "- **clauderig:** a clauderig feature") {
		t.Errorf("scope lead-in missing in:\n%s", got)
	}
	if strings.Contains(got, "feat(clauderig):") {
		t.Errorf("conventional prefix left in the prose:\n%s", got)
	}
	// Within Enhancements, clauderig sorts before rig regardless of input order.
	enh := got[strings.Index(got, "### 🚀 Enhancements"):strings.Index(got, "### 🩹 Fixes")]
	if strings.Index(enh, "**clauderig:**") > strings.Index(enh, "**rig:**") {
		t.Errorf("bullets not grouped by scope:\n%s", enh)
	}
	// An unscoped bullet carries no lead-in.
	if !strings.Contains(got, "- an unscoped tidy") {
		t.Errorf("unscoped bullet should have no lead-in:\n%s", got)
	}
}
