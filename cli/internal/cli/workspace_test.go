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

func names(ts []target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
