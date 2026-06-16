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
