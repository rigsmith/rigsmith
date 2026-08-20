package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/prestate"
)

func cs(id, summary string, names ...string) *changeset.Changeset {
	c := &changeset.Changeset{ID: id, Summary: summary}
	for _, n := range names {
		c.Releases = append(c.Releases, changeset.Release{Name: n, Bump: changeset.BumpMinor})
	}
	return c
}

// The case from the Avalloy 0.9.0 release: one changeset named two packages,
// the body was written about the new one, and the other package's consumers got
// several paragraphs about a terminal host they may not use.
func TestFindUnmentioned_TheAvalloyCase(t *testing.T) {
	body := "Adds Avalloy.Terminal.Hosting, a host for terminal sessions. " +
		"Also adds the token family it needs."
	found := FindUnmentioned([]*changeset.Changeset{
		cs("terminal-hosting-extraction", body, "Avalloy.Terminal.Hosting", "Avalloy.Themes"),
	}, nil)

	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	if got := found[0].Missing; len(got) != 1 || got[0] != "Avalloy.Themes" {
		t.Fatalf("Missing = %v, want [Avalloy.Themes]", got)
	}
	if got := found[0].Mentioned; len(got) != 1 || got[0] != "Avalloy.Terminal.Hosting" {
		t.Fatalf("Mentioned = %v, want [Avalloy.Terminal.Hosting]", got)
	}
	if hint := found[0].Hint("changerig"); !strings.Contains(hint, "add -p Avalloy.Themes") {
		t.Errorf("Hint = %q, want it to name the missing package", hint)
	}
}

func TestFindUnmentioned_Quiet(t *testing.T) {
	linked := &config.Config{Linked: [][]string{{"acme.core", "acme.ui"}}}
	for _, tc := range []struct {
		name string
		cs   *changeset.Changeset
		cfg  *config.Config
	}{
		{"single package cannot be about the wrong one",
			cs("a", "Adds a widget to acme.core", "acme.core"), nil},
		{"every package mentioned",
			cs("b", "acme.core gains a parser; acme.ui renders it", "acme.core", "acme.ui"), nil},
		{"no package mentioned at all is ordinary prose, not a mismatch",
			cs("c", "Fixes a crash when the buffer is empty.", "acme.core", "acme.ui"), nil},
		{"a lockstep family shares one body by configuration",
			cs("d", "acme.core gains a parser.", "acme.core", "acme.ui"), linked},
		{"an empty body is a different problem",
			cs("e", "   ", "acme.core", "acme.ui"), nil},
		{"mentioned by its distinctive last segment",
			cs("f", "Adds a parser to core and a renderer to ui-widgets", "acme/core", "acme/ui-widgets"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if found := FindUnmentioned([]*changeset.Changeset{tc.cs}, tc.cfg); len(found) != 0 {
				t.Fatalf("warned on %+v", found[0])
			}
		})
	}
}

// A linked family plus an unrelated package is exactly the case worth flagging,
// so the lockstep skip must require ALL the names to be in one group.
func TestFindUnmentioned_LockstepPlusAnOutsider(t *testing.T) {
	cfg := &config.Config{Linked: [][]string{{"acme.core", "acme.ui"}}}
	found := FindUnmentioned([]*changeset.Changeset{
		cs("x", "acme.core gains a parser.", "acme.core", "acme.ui", "unrelated.tool"),
	}, cfg)
	if len(found) != 1 {
		t.Fatalf("found %d, want 1 — the family is linked but unrelated.tool is not", len(found))
	}
	if got := found[0].Missing; len(got) != 2 {
		t.Fatalf("Missing = %v, want both acme.ui and unrelated.tool", got)
	}
}

// Word boundaries: a short or embedded name must not count as a mention, or the
// warning goes silent exactly when it is needed.
func TestMentionsPackage_Boundaries(t *testing.T) {
	for _, tc := range []struct {
		body, pkg string
		want      bool
	}{
		{"rebuilding the layer", "ui", false},          // only inside "rebuilding"
		{"the ui layer", "ui", true},                   // standalone
		{"adds Themes tokens", "Avalloy.Themes", true}, // by last dotted segment
		{"adds theme tokens", "Avalloy.Themes", false}, // singular is not the name
		{"see github.com/acme/widget", "github.com/acme/widget", true},
		{"the widget renders", "github.com/acme/widget", true}, // by last path segment
		{"go modules are nice", "acme/go", false},              // tail too short to be meaningful
	} {
		if got := mentionsPackage(tc.body, tc.pkg); got != tc.want {
			t.Errorf("mentionsPackage(%q, %q) = %v, want %v", tc.body, tc.pkg, got, tc.want)
		}
	}
}

func TestPrintUnmentioned_SaysWhatWillHappen(t *testing.T) {
	var buf bytes.Buffer
	PrintUnmentioned(&buf, []Unmentioned{{
		ID: "terminal-hosting-extraction", Mentioned: []string{"Avalloy.Terminal.Hosting"},
		Missing: []string{"Avalloy.Themes"},
	}}, "changerig")
	out := buf.String()
	for _, want := range []string{
		"terminal-hosting-extraction", "names 2 packages",
		`"Avalloy.Themes" will get this same text verbatim`,
		"changerig add -p Avalloy.Themes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// An ignored package is never released, so nothing is rendered for it and the
// warning's central claim — "will get this same text verbatim" — would be false.
func TestFindUnmentioned_IgnoresIgnoredPackages(t *testing.T) {
	cfg := &config.Config{Ignore: []string{"acme.internal"}}

	// One live package left: the body cannot be aimed at the wrong one.
	if found := FindUnmentioned([]*changeset.Changeset{
		cs("a", "Adds a parser to acme.core.", "acme.core", "acme.internal"),
	}, cfg); len(found) != 0 {
		t.Fatalf("warned about an ignored package: %+v", found[0])
	}

	// Two live packages plus an ignored one: still worth flagging, but the
	// ignored package must not be listed as a recipient.
	found := FindUnmentioned([]*changeset.Changeset{
		cs("b", "Adds a parser to acme.core.", "acme.core", "acme.ui", "acme.internal"),
	}, cfg)
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	if got := found[0].Missing; len(got) != 1 || got[0] != "acme.ui" {
		t.Fatalf("Missing = %v, want [acme.ui] — acme.internal receives nothing", got)
	}
}

// A prerelease keeps consumed changesets on disk. Warning about one would be
// advice about a changelog this run is not going to write.
func TestActiveChangesets_DropsConsumedPrereleaseChangesets(t *testing.T) {
	all := []*changeset.Changeset{cs("used", "x", "p"), cs("fresh", "y", "p")}

	if got := activeChangesets(all, nil); len(got) != 2 {
		t.Fatalf("no prestate: got %d, want all 2", len(got))
	}
	pre := &prestate.PreState{Mode: prestate.ModePre, Tag: "next", Changesets: []string{"used"}}
	got := activeChangesets(all, pre)
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("in pre mode: got %v, want only the unconsumed one", got)
	}
	exit := &prestate.PreState{Mode: prestate.ModeExit, Changesets: []string{"used"}}
	if got := activeChangesets(all, exit); len(got) != 2 {
		t.Fatalf("exiting pre: got %d, want all 2 (they release now)", len(got))
	}
}

// The body is judged as authored. A changelog-github run rewrites each summary
// in place with a repo URL, and `acme/widgets` in that URL would read as a
// mention of a package named widgets — silencing version where status warned.
func TestFindUnmentioned_EnrichedSummaryWouldHideTheMismatch(t *testing.T) {
	authored := "Adds a terminal host."
	enriched := authored + " ([#12](https://github.com/acme/widgets/pull/12))"

	if found := FindUnmentioned([]*changeset.Changeset{
		cs("a", authored, "acme.host", "widgets"),
	}, nil); len(found) != 1 {
		t.Fatal("the authored body does not mention widgets — it should warn")
	}
	if found := FindUnmentioned([]*changeset.Changeset{
		cs("b", enriched, "acme.host", "widgets"),
	}, nil); len(found) != 0 {
		t.Skip("enrichment no longer defeats the check; the ordering guard may be redundant")
	}
}
