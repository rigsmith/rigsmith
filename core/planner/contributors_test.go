package planner

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/semver"
)

// renderModule renders a module the way `version` does (through the builtin
// generator), so these tests exercise the real plugin path.
func renderModule(m *Module) string {
	out, _ := NewBuiltinGenerator(nil).Render(nil, ModuleToRequest(m))
	return out
}

func TestRenderContributorsSection(t *testing.T) {
	v, _ := semver.Parse("1.1.0")
	m := &Module{
		Name:        "pkg",
		DisplayName: "pkg",
		Current:     v,
		Changes:     []Change{{Description: "add a thing", Bump: changeset.BumpMinor, Type: "feat"}},
		Contributors: []plugin.Author{
			{Name: "Pooya Parsa", Login: "pi0"}, // linked
			{Name: "Jane Doe"},                  // no login → bare name, no email ever
		},
	}
	got := renderModule(m)

	if !strings.Contains(got, "### ❤️ Contributors") {
		t.Errorf("missing default Contributors heading:\n%s", got)
	}
	if !strings.Contains(got, "- Pooya Parsa ([@pi0](https://github.com/pi0))") {
		t.Errorf("missing linked contributor:\n%s", got)
	}
	if !strings.Contains(got, "- Jane Doe\n") {
		t.Errorf("missing bare-name contributor:\n%s", got)
	}
	// The Contributors section comes after the change sections.
	if strings.Index(got, "Enhancements") > strings.Index(got, "Contributors") {
		t.Errorf("Contributors should render last:\n%s", got)
	}
}

func TestRenderContributorsCustomHeadingAndOmittedWhenEmpty(t *testing.T) {
	v, _ := semver.Parse("1.0.1")
	base := Module{Name: "p", DisplayName: "p", Current: v,
		Changes: []Change{{Description: "fix", Bump: changeset.BumpPatch, Type: "fix"}}}

	// Custom heading.
	m := base
	m.Contributors = []plugin.Author{{Name: "A"}}
	m.ContributorsSection = "👏 Thanks"
	if got := renderModule(&m); !strings.Contains(got, "### 👏 Thanks") {
		t.Errorf("custom heading not used:\n%s", got)
	}

	// No contributors → no section at all.
	if got := renderModule(&base); strings.Contains(got, "Contributors") {
		t.Errorf("empty contributors should render no section:\n%s", got)
	}
}
