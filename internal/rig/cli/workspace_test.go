package cli

import "testing"

func TestTopoSortDepsFirst(t *testing.T) {
	// web → app → core. Order must place core before app before web.
	targets := []target{
		{Name: "web", Deps: []string{"app"}},
		{Name: "app", Deps: []string{"core"}},
		{Name: "core"},
	}
	order := topoSort(targets)
	pos := map[string]int{}
	for i, o := range order {
		pos[o.Name] = i
	}
	if !(pos["core"] < pos["app"] && pos["app"] < pos["web"]) {
		t.Fatalf("bad topo order: %v", names(order))
	}
}

func TestTopoSortCycleTolerant(t *testing.T) {
	targets := []target{{Name: "a", Deps: []string{"b"}}, {Name: "b", Deps: []string{"a"}}}
	if got := topoSort(targets); len(got) != 2 {
		t.Fatalf("cycle dropped targets: %v", names(got))
	}
}

func TestFilterTargets(t *testing.T) {
	targets := []target{{Name: "@x/core"}, {Name: "@x/app"}, {Name: "@x/app-bench"}}
	got := filterTargets(targets, "*app*")
	if len(got) != 2 {
		t.Errorf("filter *app* = %v, want 2", names(got))
	}
}

func TestMatchTarget(t *testing.T) {
	targets := []target{
		{Name: "github.com/me/core", Dir: "/r/core"},
		{Name: "github.com/me/app", Dir: "/r/app"},
	}
	if m, ok := matchTarget(targets, "app"); !ok || m.Name != "github.com/me/app" {
		t.Errorf("short-name match failed: %+v ok=%v", m, ok)
	}
	if m, ok := matchTarget(targets, "github.com/me/core"); !ok || m.Dir != "/r/core" {
		t.Errorf("full-name match failed: %+v", m)
	}
	if _, ok := matchTarget(targets, "nope"); ok {
		t.Error("expected no match for 'nope'")
	}
}

func TestMatchTargets(t *testing.T) {
	// The same project checked out in two paths (e.g. a nested worktree): a
	// dot-short query must surface BOTH, not silently collapse to one.
	targets := []target{
		{Name: "Tweed.App2", Dir: "/r/ui/src/Tweed.App2"},
		{Name: "Tweed.App2", Dir: "/r/.claude/worktrees/x/ui/src/Tweed.App2"},
		{Name: "Tweed.App", Dir: "/r/ui/src/Tweed.App"},
	}
	if m := matchTargets(targets, "App2"); len(m) != 2 {
		t.Fatalf("dot-short duplicate = %v, want 2 matches", names(m))
	}
	// matchTarget stays single-or-nothing: a duplicate is ambiguous.
	if _, ok := matchTarget(targets, "App2"); ok {
		t.Error("matchTarget should report ambiguous (ok=false) for a duplicate name")
	}
	// A dot-short that resolves uniquely still matches the one target.
	if m := matchTargets(targets, "App"); len(m) != 1 || m[0].Name != "Tweed.App" {
		t.Errorf("dot-short unique = %v, want [Tweed.App]", names(m))
	}
	// Exact matches win over substring: "Tweed.App" is exact for Tweed.App even
	// though it's a substring of both Tweed.App2 entries.
	if m := matchTargets(targets, "Tweed.App"); len(m) != 1 || m[0].Name != "Tweed.App" {
		t.Errorf("exact-over-substring = %v, want [Tweed.App]", names(m))
	}
	if m := matchTargets(targets, "nope"); len(m) != 0 {
		t.Errorf("no match = %v, want none", names(m))
	}
	if m := matchTargets(targets, ""); m != nil {
		t.Errorf("empty query = %v, want nil", names(m))
	}
}

func TestTopoSortKeepsDuplicates(t *testing.T) {
	// Duplicate-named targets in different paths must both survive topoSort (they
	// used to collapse to one), while dependency order still holds.
	targets := []target{
		{Name: "app", Dir: "/a", Deps: []string{"core"}},
		{Name: "app", Dir: "/b", Deps: []string{"core"}},
		{Name: "core", Dir: "/core"},
	}
	order := topoSort(targets)
	if len(order) != 3 {
		t.Fatalf("topoSort dropped a duplicate: %v", names(order))
	}
	var coreAt, lastApp int
	for i, o := range order {
		switch o.Name {
		case "core":
			coreAt = i
		case "app":
			lastApp = i
		}
	}
	if coreAt > lastApp {
		t.Fatalf("core must precede its dependents: %v", names(order))
	}
}

func TestDuplicateNames(t *testing.T) {
	dup := duplicateNames([]target{
		{Name: "app", Dir: "/a"},
		{Name: "app", Dir: "/b"},
		{Name: "core", Dir: "/c"},
	})
	if !dup["app"] || dup["core"] {
		t.Errorf("duplicateNames = %v, want only app", dup)
	}
}

func TestMatchDefaultProject(t *testing.T) {
	// The .NET case that matchTarget's substring fallback gets wrong: a dot-short
	// default must scope to the app, not go ambiguous against its .Tests sibling.
	targets := []target{
		{Name: "Acme.Desktop", Dir: "/r/Acme.Desktop"},
		{Name: "Acme.Desktop.Tests", Dir: "/r/Acme.Desktop.Tests"},
	}
	if m, ok := matchDefaultProject(targets, "Desktop"); !ok || m.Name != "Acme.Desktop" {
		t.Errorf("dot-short default = %+v ok=%v, want Acme.Desktop", m, ok)
	}
	if m, ok := matchDefaultProject(targets, "acme.desktop"); !ok || m.Name != "Acme.Desktop" {
		t.Errorf("full-name (case-insensitive) default = %+v ok=%v", m, ok)
	}
	// Substring-only would match both — matchDefaultProject must NOT.
	if _, ok := matchDefaultProject([]target{{Name: "Acme.DesktopApp"}, {Name: "Acme.DesktopTools"}}, "desktop"); ok {
		t.Error("bare substring should not resolve a default (no exact full/short/dot-short)")
	}
	if _, ok := matchDefaultProject(targets, ""); ok {
		t.Error("empty default should not match")
	}
}

func names(ts []target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
