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
	got := renderSections("1.1.0", changes, config.DefaultChangelogGroups, nil)

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

// The configured scope order decides which tool leads inside a section;
// scopes left out of the list keep alphabetical order after the listed ones,
// and unscoped entries stay last.
func TestScopeOrderIsConfigurable(t *testing.T) {
	changes := []plugin.ChangelogChange{
		{Bump: "minor", Type: "feat", Scope: "clauderig", Summary: "feat(clauderig): c"},
		{Bump: "minor", Type: "feat", Summary: "feat: unscoped"},
		{Bump: "minor", Type: "feat", Scope: "shiprig", Summary: "feat(shiprig): s"},
		{Bump: "minor", Type: "feat", Scope: "rig", Summary: "feat(rig): r"},
	}
	got := renderSections("1.1.0", changes, config.DefaultChangelogGroups, []string{"rig", "clauderig"})

	want := []string{"**rig:** r", "**clauderig:** c", "**shiprig:** s", "- unscoped"}
	at := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
		if i < at {
			t.Errorf("%q out of order in:\n%s", w, got)
		}
		at = i
	}
}

// A summary that ends with newlines must not render indented blank lines under
// its bullet — every entry read out of a file ends that way.
func TestTrailingNewlinesDoNotBecomeBlankContinuations(t *testing.T) {
	got := renderSections("1.0.1", []plugin.ChangelogChange{
		{Bump: "patch", Type: "fix", Summary: "fix: a thing\n\n"},
	}, config.DefaultChangelogGroups, nil)
	if strings.Contains(got, "\n  \n") || strings.HasSuffix(got, "  \n") {
		t.Errorf("blank continuation line rendered:\n%q", got)
	}
}
