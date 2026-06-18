package cli

import (
	"context"
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

func TestDevVerbCmd_InteractiveFlagScope(t *testing.T) {
	// run and the --all-capable verbs expose -i/--interactive…
	if devVerbCmd("run", "", false).Flags().Lookup("interactive") == nil {
		t.Error("`rig run` should expose an --interactive flag")
	}
	if devVerbCmd("build", "", true).Flags().Lookup("interactive") == nil {
		t.Error("`rig build` (an --all verb) should expose an --interactive flag")
	}
	// …but a single-target verb like rebuild (no --all, not run) does not.
	if f := devVerbCmd("rebuild", "", false).Flags().Lookup("interactive"); f != nil {
		t.Error("rebuild has no workspace picker, so no --interactive")
	}
}

func TestOfferWorkspaceChoice_ForcePickEmptyErrors(t *testing.T) {
	host, _ := newRunHost()
	handled, err := offerWorkspaceChoice(host, t.TempDir(), "run", false, true)
	if !handled {
		t.Fatal("-i/--interactive must handle the run (it never falls through to a root command)")
	}
	if err == nil || !strings.Contains(err.Error(), "nothing runnable") {
		t.Fatalf("-i/--interactive on an empty root err = %v, want a nothing-runnable error", err)
	}
}

func TestOfferWorkspaceChoice_ForcePickEmptyVerb(t *testing.T) {
	host, _ := newRunHost()
	handled, err := offerWorkspaceChoice(host, t.TempDir(), "build", true, true)
	if !handled {
		t.Fatal("-i/--interactive must handle the verb (never falls through to a root command)")
	}
	if err == nil || !strings.Contains(err.Error(), "no build targets") {
		t.Fatalf("`build -i` on an empty root err = %v, want a no-targets error", err)
	}
}

// A Go module whose mains live under cmd/ has no `package main` at the module
// root, so the root target is not runnable. `rig run` there must not treat it
// as a runnable root package — doing so suppressed the picker + surfaced
// scripts and fell through to a doomed `go run .` ("no Go files in <root>").
func TestOfferWorkspaceChoice_NonRunnableRootSurfacesScripts(t *testing.T) {
	isolateGlobalConfig(t)
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

// countExcluded counts the visible rows the picker marks excluded.
func countExcluded(m runPickerModel) int {
	n := 0
	for _, r := range m.flat {
		if r.excluded {
			n++
		}
	}
	return n
}

// The live run picker excludes a crowded directory via the whole-dir prompt,
// hides those rows, reveals them under show-all, and re-includes them — each
// writing the repo .rig.json.
func TestRunPickerLive_ExcludeWholeDirShowAllAndInclude(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/api", "main")
	writeGoPkg(t, root, "cmd/worker", "main")
	for i := 0; i < 6; i++ {
		writeGoPkg(t, root, "examples/e"+string(rune('0'+i)), "main")
	}

	m := newRunPickerLive(context.Background(), root, nil)
	if len(m.flat) != 8 { // 2 cmd + 6 examples, none excluded yet
		t.Fatalf("initial rows = %d, want 8: %v", len(m.flat), m.flat)
	}

	// Cursor 0=api,1=worker,2=examples/e0 (goMainDirs sorts the dirs).
	m = rp(rp(m, wtKeyMsg("down")), wtKeyMsg("down"))
	if got := m.flat[m.cursor].path; got != "examples/e0" {
		t.Fatalf("cursor on %q, want examples/e0", got)
	}

	// x on a crowded dir opens the just/dir prompt; d excludes the whole dir.
	m = rp(m, wtKeyMsg("x"))
	if m.pending == nil || m.pending.dirGlob != "examples/*" {
		t.Fatalf("expected the whole-dir prompt for examples/*, got %+v", m.pending)
	}
	m = rp(m, wtKeyMsg("d"))
	if m.pending != nil {
		t.Fatal("prompt should clear after choosing")
	}
	if len(m.flat) != 2 { // examples hidden
		t.Fatalf("after exclude, rows = %d, want 2", len(m.flat))
	}
	if cfg, _ := config.LoadMerged(root); len(cfg.Exclude) != 1 || cfg.Exclude[0] != "examples/*" {
		t.Fatalf(".rig.json exclude = %v, want [examples/*]", excludeFor(root))
	}

	// show-all reveals the 6 excluded rows (struck through).
	m = rp(m, wtKeyMsg("a"))
	if len(m.flat) != 8 || countExcluded(m) != 6 {
		t.Fatalf("show-all rows = %d (excluded %d), want 8 (6)", len(m.flat), countExcluded(m))
	}

	// Re-include from an excluded row drops the directory glob.
	for m.flat[m.cursor].path != "examples/e0" {
		m = rp(m, wtKeyMsg("down"))
	}
	m = rp(m, wtKeyMsg("i"))
	if cfg := excludeFor(root); len(cfg) != 0 {
		t.Fatalf("after include, exclude = %v, want empty", cfg)
	}
	if countExcluded(m) != 0 {
		t.Fatalf("after include, %d rows still marked excluded", countExcluded(m))
	}
}

// Enter in the live picker resolves the highlighted project to a runnable task.
func TestRunPickerLive_EnterSelectsProject(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/api", "main")
	writeGoPkg(t, root, "cmd/worker", "main")

	m := newRunPickerLive(context.Background(), root, nil)
	m = rp(m, wtKeyMsg("enter")) // cursor 0 = cmd/api
	if m.chosen == nil || m.chosen.task == nil || m.chosen.task.name != "api" {
		t.Fatalf("enter should choose api, got %+v", m.chosen)
	}
	if argv := strings.Join(m.chosen.task.argv, " "); !strings.Contains(argv, "go run") {
		t.Fatalf("api argv = %q, want a `go run`", argv)
	}
}

func rowPaths(m runPickerModel) []string {
	out := make([]string, len(m.flat))
	for i, r := range m.flat {
		out[i] = r.path
	}
	return out
}

// The live picker sorts by path by default, toggles to ecosystem grouping on
// `e`, and narrows by name under `/`.
func TestRunPickerLive_SortAndFilter(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGoPkg(t, root, "cmd/alpha", "main")
	writeGoPkg(t, root, "cmd/zebra", "main")
	// A node package whose path sorts before the Go binaries.
	if err := os.MkdirAll(filepath.Join(root, "aweb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "aweb", "package.json"),
		[]byte(`{"name":"aweb","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newRunPickerLive(context.Background(), root, nil)
	// Default: by path → aweb, cmd/alpha, cmd/zebra.
	if got := rowPaths(m); strings.Join(got, ",") != "aweb,cmd/alpha,cmd/zebra" {
		t.Fatalf("default order = %v, want aweb,cmd/alpha,cmd/zebra", got)
	}

	// e → by ecosystem (go before node), path as tiebreak.
	m = rp(m, wtKeyMsg("e"))
	if got := rowPaths(m); strings.Join(got, ",") != "cmd/alpha,cmd/zebra,aweb" {
		t.Fatalf("eco order = %v, want cmd/alpha,cmd/zebra,aweb", got)
	}

	// / then "alp" narrows to the one match.
	m = rp(m, wtKeyMsg("/"))
	if !m.filtering {
		t.Fatal("/ should enter filter mode")
	}
	m = rp(m, wtKeyMsg("alp"))
	if len(m.flat) != 1 || m.flat[0].name != "alpha" {
		t.Fatalf("filter 'alp' = %v, want only alpha", rowPaths(m))
	}
	// esc clears the filter.
	m = rp(m, key(tea.KeyEsc))
	if m.filtering || m.query != "" || len(m.flat) != 3 {
		t.Fatalf("esc should clear the filter; rows=%v filtering=%v", rowPaths(m), m.filtering)
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
