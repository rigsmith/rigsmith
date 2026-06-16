package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/internal/rig/config"
)

func rp(m runPickerModel, msg tea.Msg) runPickerModel {
	nm, _ := m.Update(msg)
	return nm.(runPickerModel)
}

func twoSections() []runPickSection {
	return []runPickSection{
		{title: "Projects", rows: []runPickRow{
			{name: "api", eco: "go", path: "services/api", index: 0},
			{name: "web", eco: "node", path: "apps/web", index: 1},
		}},
		{title: "Scripts", rows: []runPickRow{
			{name: "deploy", eco: "custom", path: ".rig.json", script: true, index: 0},
			{name: "gen", eco: "go", path: "scripts/gen", script: true, index: 1},
		}},
	}
}

func TestRunPicker_FlattensSectionsInOrder(t *testing.T) {
	m := newRunPickerModel(twoSections())
	if len(m.flat) != 4 {
		t.Fatalf("flat rows = %d, want 4", len(m.flat))
	}
	// Projects come before scripts, in their given order.
	want := []string{"api", "web", "deploy", "gen"}
	for i, w := range want {
		if m.flat[i].name != w {
			t.Errorf("flat[%d] = %q, want %q", i, m.flat[i].name, w)
		}
	}
}

func TestRunPicker_SelectsProjectAndScript(t *testing.T) {
	// Cursor starts on the first project.
	m := newRunPickerModel(twoSections())
	if r := m.flat[m.cursor]; r.script || r.index != 0 {
		t.Fatalf("default selection = %+v, want project index 0", r)
	}

	// Down three times lands on the second script (gen, script index 1).
	m = rp(rp(rp(m, key(tea.KeyDown)), key(tea.KeyDown)), key(tea.KeyDown))
	r := m.flat[m.cursor]
	if !r.script || r.index != 1 {
		t.Fatalf("after 3×down, selection = %+v, want script index 1", r)
	}

	// Cursor is clamped at the last row.
	m = rp(m, key(tea.KeyDown))
	if m.cursor != 3 {
		t.Errorf("cursor past end = %d, want clamped at 3", m.cursor)
	}
}

func TestRunPicker_CancelOnQuit(t *testing.T) {
	m := rp(newRunPickerModel(twoSections()), key(tea.KeyCtrlC))
	if !m.cancelled {
		t.Error("ctrl+c should cancel")
	}
}

func TestRunPicker_ViewShowsGroupsAndColumns(t *testing.T) {
	v := newRunPickerModel(twoSections()).View()
	for _, want := range []string{"Projects", "Scripts", "api", "deploy", "services/api", "scripts/gen"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestPickColumns_AlignsNameAndEco(t *testing.T) {
	got := pickColumns("api", "go", "services/api", 6, 4)
	if want := "api     go    services/api"; got != want {
		t.Errorf("pickColumns = %q, want %q", got, want)
	}
}

func TestDevVerbCmd_PickFlagScope(t *testing.T) {
	// run and the --all-capable verbs expose --pick…
	if devVerbCmd("run", "", false).Flags().Lookup("pick") == nil {
		t.Error("`rig run` should expose a --pick flag")
	}
	if devVerbCmd("build", "", true).Flags().Lookup("pick") == nil {
		t.Error("`rig build` (an --all verb) should expose a --pick flag")
	}
	// …but a single-target verb like rebuild (no --all, not run) does not.
	if f := devVerbCmd("rebuild", "", false).Flags().Lookup("pick"); f != nil {
		t.Error("rebuild has no workspace picker, so no --pick")
	}
}

func TestOfferWorkspaceChoice_ForcePickEmptyErrors(t *testing.T) {
	host, _ := newRunHost()
	handled, err := offerWorkspaceChoice(host, t.TempDir(), "run", false, true)
	if !handled {
		t.Fatal("--pick must handle the run (it never falls through to a root command)")
	}
	if err == nil || !strings.Contains(err.Error(), "nothing runnable") {
		t.Fatalf("--pick on an empty root err = %v, want a nothing-runnable error", err)
	}
}

func TestOfferWorkspaceChoice_ForcePickEmptyVerb(t *testing.T) {
	host, _ := newRunHost()
	handled, err := offerWorkspaceChoice(host, t.TempDir(), "build", true, true)
	if !handled {
		t.Fatal("--pick must handle the verb (never falls through to a root command)")
	}
	if err == nil || !strings.Contains(err.Error(), "no build targets") {
		t.Fatalf("`build --pick` on an empty root err = %v, want a no-targets error", err)
	}
}

// A Go module whose mains live under cmd/ has no `package main` at the module
// root, so the root target is not runnable. `rig run` there must not treat it
// as a runnable root package — doing so suppressed the picker + surfaced
// scripts and fell through to a doomed `go run .` ("no Go files in <root>").
func TestOfferWorkspaceChoice_NonRunnableRootSurfacesScripts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The real binary lives under cmd/ (not a run target on its own), and there
	// is no package main at the module root.
	writeGoPkg(t, root, "cmd/app", "main")
	// Two on-disk scripts so the resolution lands on the picker path rather than
	// auto-running a lone target.
	writeGoPkg(t, root, "scripts/gen", "main")
	writeGoPkg(t, root, "scripts/seed", "main")

	host, _ := newRunHost()
	handled, err := offerWorkspaceChoice(host, root, "run", false, false)

	// The fix: rig handles the run (offers the scripts) instead of returning
	// handled=false and letting the doomed `go run .` root command run.
	if !handled {
		t.Fatal("a non-runnable Go root must not fall through to the root `go run .` command")
	}
	// Off a TTY there's no picker, but the error enumerates what was surfaced:
	// cmd/app is the one Project; gen and seed are the two scripts — deduped out
	// of the Projects group, so it's "1 package", not "3 packages".
	if err == nil {
		t.Fatal("want a no-single-target error, got nil")
	}
	for _, want := range []string{"1 package", "2 scripts"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestDiscoverScripts_AggregatesSourcesWithPrecedence(t *testing.T) {
	root := t.TempDir()
	// A package.json script and a Go scripts/ verb.
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"deploy":"echo node-deploy","front":"vite"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "scripts/gen", "main")
	if err := os.WriteFile(filepath.Join(root, "go.work"),
		[]byte("go 1.26\n\nuse ./scripts/gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A custom command that shadows the package.json "deploy" (custom wins).
	cfg := config.Config{
		Path: filepath.Join(root, ".rig.json"),
		Commands: map[string]*config.Command{
			"deploy": {Spec: &config.CommandSpec{Shell: "echo custom-deploy"}},
		},
	}

	entries := discoverScripts(root, cfg)
	byName := map[string]scriptEntry{}
	for _, e := range entries {
		byName[e.name] = e
	}

	if got := byName["deploy"]; got.eco != "custom" || got.loc != ".rig.json" {
		t.Errorf("deploy = %+v, want custom command (eco=custom, loc=.rig.json) winning over package.json", got)
	}
	if got := byName["front"]; got.eco != "node" || got.loc != "package.json" {
		t.Errorf("front = %+v, want node/package.json", got)
	}
	if got := byName["gen"]; got.eco != "go" || got.loc != "scripts/gen" {
		t.Errorf("gen = %+v, want go/scripts/gen", got)
	}
	if len(entries) != 3 {
		t.Errorf("entries = %d (%v), want 3 deduped", len(entries), entries)
	}
}
