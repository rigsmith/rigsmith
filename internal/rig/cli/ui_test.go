// Port of the .NET rig's MenuInputTests (the menu's Esc/Backspace cancel keys,
// parity with the Node menu), mapped onto the bubbletea menuModel: in Go the
// cancel keys live in menuModel.Update rather than a wrapped console input, so
// the tests drive Update with scripted key messages — no TTY needed. The .NET
// suite's separate async-read case has no analogue here (bubbletea has a
// single synchronous Update path), so it is covered by the same tests.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func mu(m menuModel, msg tea.Msg) menuModel {
	nm, _ := m.Update(msg)
	return nm.(menuModel)
}

// scriptedMenu is a two-level menu with deterministic items (newMenu probes
// the cwd's ecosystems, which a unit test must not depend on).
func scriptedMenu() menuModel {
	return menuModel{
		stack: []frame{{items: []menuItem{
			{label: "build", verb: "build"},
			{label: "test", verb: "test"},
			{label: "▸ More", children: []menuItem{{label: "kill", verb: "kill"}}},
		}}},
	}
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// isQuit reports whether cmd is tea.Quit.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// `w` toggles watch at the top level; the flag rides into dispatch so the next
// dev verb runs in watch mode.
func TestMenu_WTogglesWatch(t *testing.T) {
	m := scriptedMenu()
	if m.watch {
		t.Fatal("watch should start off")
	}
	if m = mu(m, runes("w")); !m.watch {
		t.Error("w should turn watch on")
	}
	if m = mu(m, runes("w")); m.watch {
		t.Error("w again should turn watch off")
	}
}

// The coverage "browse" menu pick pre-sets the interactive flag (coverage -i).
func TestCoverageMenuCmd_BrowseSetsInteractive(t *testing.T) {
	if got := coverageMenuCmd(false).Flags().Lookup("interactive").Value.String(); got != "false" {
		t.Errorf("summary interactive = %q, want false", got)
	}
	if got := coverageMenuCmd(true).Flags().Lookup("interactive").Value.String(); got != "true" {
		t.Errorf("browse interactive = %q, want true", got)
	}
}

func TestMenu_EscapeCancelsThePrompt(t *testing.T) {
	model, cmd := scriptedMenu().Update(key(tea.KeyEscape))
	if !isQuit(cmd) {
		t.Fatal("esc at the top level must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "" {
		t.Fatalf("chosen = %q, want empty (cancelled, nothing runs)", chosen)
	}
}

func TestMenu_BackspaceCancelsThePrompt(t *testing.T) {
	model, cmd := scriptedMenu().Update(key(tea.KeyBackspace))
	if !isQuit(cmd) {
		t.Fatal("backspace at the top level must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "" {
		t.Fatalf("chosen = %q, want empty", chosen)
	}
}

func TestMenu_EscapePopsASubmenuBeforeCancelling(t *testing.T) {
	m := scriptedMenu()
	// Navigate into the "▸ More" group: down ×2, enter.
	var model tea.Model = m
	for _, k := range []tea.KeyType{tea.KeyDown, tea.KeyDown, tea.KeyEnter} {
		model, _ = model.(menuModel).Update(key(k))
	}
	if depth := len(model.(menuModel).stack); depth != 2 {
		t.Fatalf("stack depth = %d, want 2 (inside the submenu)", depth)
	}

	// Esc pops back to the top level instead of quitting…
	model, cmd := model.(menuModel).Update(key(tea.KeyEscape))
	if isQuit(cmd) {
		t.Fatal("esc inside a submenu must go back, not quit")
	}
	if depth := len(model.(menuModel).stack); depth != 1 {
		t.Fatalf("stack depth = %d, want 1 after esc", depth)
	}
	// …and a second esc cancels for real.
	if _, cmd = model.(menuModel).Update(key(tea.KeyEscape)); !isQuit(cmd) {
		t.Fatal("esc at the top level must quit")
	}
}

// scriptedFocusMenu is a menu with a project picker entry and two verbs,
// mirroring newMenu's layout in a multi-project repo.
func scriptedFocusMenu() menuModel {
	return menuModel{
		projects: []projectRow{{name: "App", rel: "App"}, {name: "Worker", rel: "Worker"}},
		stack: []frame{{items: []menuItem{
			{pickFocus: true},
			{label: "build", verb: "build"},
			{label: "test", verb: "test"},
		}}},
	}
}

// The menu's project picker excludes a crowded directory (prompting just/dir),
// hides those rows, reveals them under show-all, and re-includes them — each
// writing the repo .rig.json. Uses node packages (discovered per-package).
func TestMenu_ProjectPickerExcludeShowAllInclude(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writePkg := func(rel, name string) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"),
			[]byte(`{"name":"`+name+`","version":"1.0.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePkg("core", "core")
	for i := 0; i < 6; i++ {
		writePkg("examples/e"+string(rune('0'+i)), "ex"+string(rune('0'+i)))
	}

	m := menuModel{root: root, projects: menuProjectEntries(root)}
	m.stack = []frame{
		{items: []menuItem{{pickFocus: true}}},
		{title: "▸ Project", items: m.projectFrameItems(), projects: true},
	}
	// Items: (whole repo), core, ex0..ex5 → 8.
	if got := len(m.top().items); got != 8 {
		t.Fatalf("picker items = %d, want 8", got)
	}

	// Cursor to ex0 (index 2), exclude → crowded prompt → whole dir.
	m = mu(mu(m, key(tea.KeyDown)), key(tea.KeyDown))
	if m.top().items[m.top().cursor].focusName != "ex0" {
		t.Fatalf("cursor on %q, want ex0", m.top().items[m.top().cursor].focusName)
	}
	m = mu(m, wtKeyMsg("x"))
	if m.pending == nil || m.pending.dirGlob != "examples/*" {
		t.Fatalf("expected examples/* whole-dir prompt, got %+v", m.pending)
	}
	m = mu(m, wtKeyMsg("d"))
	if m.pending != nil {
		t.Fatal("prompt should clear")
	}
	if got := len(m.top().items); got != 2 { // (whole repo), core
		t.Fatalf("after exclude, items = %d, want 2", got)
	}
	if cfg := excludeFor(root); len(cfg) != 1 || cfg[0] != "examples/*" {
		t.Fatalf("exclude = %v, want [examples/*]", cfg)
	}

	// show-all reveals the excluded rows.
	m = mu(m, wtKeyMsg("a"))
	if got := len(m.top().items); got != 8 {
		t.Fatalf("show-all items = %d, want 8", got)
	}

	// Include from an excluded row drops the dir glob.
	for m.top().items[m.top().cursor].focusName != "ex0" {
		m = mu(m, key(tea.KeyDown))
	}
	m = mu(m, wtKeyMsg("i"))
	if cfg := excludeFor(root); len(cfg) != 0 {
		t.Fatalf("after include, exclude = %v, want empty", cfg)
	}
}

func projFocusNames(m menuModel) []string {
	var out []string
	for _, it := range m.top().items {
		if it.focusName != "" {
			out = append(out, it.focusName)
		}
	}
	return out
}

// The menu's project picker sorts by path by default, groups by ecosystem on
// `e`, and narrows by name under `/`.
func TestMenu_ProjectPickerSortAndFilter(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writePkg := func(rel, name string) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"),
			[]byte(`{"name":"`+name+`","version":"1.0.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePkg("aweb", "aweb") // node
	writePkg("zlib", "zlib") // node
	writeGoMod(t, filepath.Join(root, "core"), "core")

	m := menuModel{root: root, projects: menuProjectEntries(root)}
	m.stack = []frame{
		{items: []menuItem{{pickFocus: true}}},
		{title: "▸ Project", items: m.projectFrameItems(), projects: true},
	}

	// Default: by path.
	if got := strings.Join(projFocusNames(m), ","); got != "aweb,core,zlib" {
		t.Fatalf("default order = %q, want aweb,core,zlib", got)
	}
	// e → by ecosystem (go before node).
	m = mu(m, wtKeyMsg("e"))
	if got := strings.Join(projFocusNames(m), ","); got != "core,aweb,zlib" {
		t.Fatalf("eco order = %q, want core,aweb,zlib", got)
	}
	// / then "we" narrows to aweb.
	m = mu(m, wtKeyMsg("/"))
	if !m.filtering {
		t.Fatal("/ should enter filter mode")
	}
	m = mu(m, wtKeyMsg("we"))
	if got := strings.Join(projFocusNames(m), ","); got != "aweb" {
		t.Fatalf("filter 'we' = %q, want aweb", got)
	}
	// esc clears the filter.
	m = mu(m, key(tea.KeyEsc))
	if m.filtering || m.query != "" {
		t.Fatalf("esc should clear the filter (filtering=%v query=%q)", m.filtering, m.query)
	}
}

func TestMenu_FocusPickerListsWholeRepoThenProjects(t *testing.T) {
	model, cmd := scriptedFocusMenu().Update(key(tea.KeyEnter)) // open the picker
	if cmd != nil {
		t.Fatal("opening the picker must not produce a command")
	}
	m := model.(menuModel)
	if depth := len(m.stack); depth != 2 {
		t.Fatalf("stack depth = %d, want 2 (picker pushed)", depth)
	}
	items := m.stack[1].items
	if len(items) != 3 || !items[0].clearFocus || items[1].focusName != "App" || items[2].focusName != "Worker" {
		t.Fatalf("picker items = %+v, want (whole repo), App, Worker", items)
	}
}

func TestMenu_PickingAProjectSetsTheFocusAndReturns(t *testing.T) {
	var model tea.Model = scriptedFocusMenu()
	for _, k := range []tea.KeyType{tea.KeyEnter, tea.KeyDown, tea.KeyEnter} { // open, down to App, pick
		model, _ = model.(menuModel).Update(key(k))
	}
	m := model.(menuModel)
	if m.focus != "App" {
		t.Fatalf("focus = %q, want App", m.focus)
	}
	if depth := len(m.stack); depth != 1 {
		t.Fatalf("stack depth = %d, want 1 (picker popped)", depth)
	}
}

func TestMenu_FocusedVerbSelectionCarriesTheFocus(t *testing.T) {
	var model tea.Model = scriptedFocusMenu()
	// Focus Worker, then run "test" (cursor stays on the picker row after the
	// pop, so: down ×2 to test).
	for _, k := range []tea.KeyType{tea.KeyEnter, tea.KeyDown, tea.KeyDown, tea.KeyEnter, tea.KeyDown, tea.KeyDown, tea.KeyEnter} {
		model, _ = model.(menuModel).Update(key(k))
	}
	m := model.(menuModel)
	if m.chosen != "test" || m.focus != "Worker" {
		t.Fatalf("chosen = %q focus = %q, want test/Worker", m.chosen, m.focus)
	}
}

func TestMenu_WholeRepoEntryClearsTheFocus(t *testing.T) {
	m := scriptedFocusMenu()
	m.focus = "App"
	var model tea.Model = m
	for _, k := range []tea.KeyType{tea.KeyEnter, tea.KeyEnter} { // open picker, pick "(whole repo)"
		model, _ = model.(menuModel).Update(key(k))
	}
	got := model.(menuModel)
	if got.focus != "" {
		t.Fatalf("focus = %q, want cleared", got.focus)
	}
	if depth := len(got.stack); depth != 1 {
		t.Fatalf("stack depth = %d, want 1", depth)
	}
}

func TestMenu_ViewShowsTheFocusInTitleAndPickerRow(t *testing.T) {
	m := scriptedFocusMenu()
	if view := m.View(); !strings.Contains(view, "project: (all)") {
		t.Fatalf("unfocused view must show the picker row as project: (all)\n%s", view)
	}
	m.focus = "App"
	view := m.View()
	if !strings.Contains(view, "project: App") {
		t.Fatalf("focused view must show project: App on the picker row\n%s", view)
	}
	if !strings.Contains(view, "rig") || !strings.Contains(view, "App") {
		t.Fatalf("focused view must carry the focus in the header\n%s", view)
	}
}

func TestMenu_EscapePopsTheFocusPickerWithoutFocusing(t *testing.T) {
	var model tea.Model = scriptedFocusMenu()
	model, _ = model.(menuModel).Update(key(tea.KeyEnter)) // open picker
	model, cmd := model.(menuModel).Update(key(tea.KeyEscape))
	if isQuit(cmd) {
		t.Fatal("esc in the picker must go back, not quit")
	}
	m := model.(menuModel)
	if m.focus != "" || len(m.stack) != 1 {
		t.Fatalf("focus = %q depth = %d, want unfocused at the top level", m.focus, len(m.stack))
	}
}

func TestMenu_OtherKeysPassThroughUntouched(t *testing.T) {
	// Down moves the cursor without quitting; enter then selects that verb.
	model, cmd := scriptedMenu().Update(key(tea.KeyDown))
	if cmd != nil {
		t.Fatal("a movement key must not produce a command")
	}
	if cursor := model.(menuModel).stack[0].cursor; cursor != 1 {
		t.Fatalf("cursor = %d, want 1", cursor)
	}

	model, cmd = model.(menuModel).Update(key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("enter on an action must quit the menu")
	}
	if chosen := model.(menuModel).chosen; chosen != "test" {
		t.Fatalf("chosen = %q, want test", chosen)
	}
}

// The top-level view surfaces the next-step line and tags the recommended item
// ("init" when there's no .rig.json), so the suggested step is visible.
func TestMenu_NextStepAndRecommendedTag(t *testing.T) {
	m := menuModel{
		nextStep: "No .rig.json yet — init pins conventions and adds custom verbs.",
		stack: []frame{{items: []menuItem{
			{label: "init", verb: "init", recommended: true},
			{label: "build", verb: "build"},
		}}},
	}
	view := m.View()
	for _, want := range []string{"No .rig.json yet", "next", "init", "build"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

// The menu renders rig's brand banner above it (the same header --help/version
// show), so a bare `rig` is self-identifying.
func TestMenu_ShowsBanner(t *testing.T) {
	if view := scriptedMenu().View(); !strings.Contains(view, "convention-first dev launcher") {
		t.Errorf("menu should render the rig banner above it\n%s", view)
	}
}

// Selecting a verb erases the menu (empty view on the quitting frame), so the
// dispatched command's output starts clean instead of below a stale menu; a
// plain quit keeps the menu in scrollback.
func TestMenu_ClearsOnSelect(t *testing.T) {
	m := menuModel{stack: []frame{{items: []menuItem{{label: "build", verb: "build"}}}}}
	sel, _ := m.Update(key(tea.KeyEnter))
	if sel.(menuModel).View() != "" {
		t.Error("menu should render empty after a verb is chosen")
	}
	q, _ := m.Update(key(tea.KeyEscape))
	if q.(menuModel).View() == "" {
		t.Error("plain quit should keep the menu rendered")
	}
}

// A project command (custom/script verb) carries its own prebuilt command;
// selecting it records that command to run on exit, not a built-in verb.
func TestMenu_ProjectCommandCarriesItsCommand(t *testing.T) {
	c := &cobra.Command{Use: "deploy", Short: "deploy it", RunE: func(*cobra.Command, []string) error { return nil }}
	m := menuModel{stack: []frame{{items: []menuItem{{label: "deploy", cmd: c}}}}}
	model, cmd := m.Update(key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("enter on a project command must quit the menu")
	}
	got := model.(menuModel)
	if got.chosenCmd == nil || got.chosenCmd.Name() != "deploy" {
		t.Fatalf("chosenCmd = %v, want the deploy command", got.chosenCmd)
	}
	if got.chosen != "" {
		t.Errorf("chosen verb = %q, want empty (a carried command, not a verb)", got.chosen)
	}
}

// The next-step line is about the repo, so it shows only at the top level — a
// submenu keeps the bare header.
func TestMenu_NextStepHiddenInSubmenu(t *testing.T) {
	m := menuModel{
		nextStep: "SHOULD-NOT-APPEAR",
		stack: []frame{
			{items: []menuItem{{label: "▸ More", children: []menuItem{{label: "kill", verb: "kill"}}}}},
			{title: "▸ More", items: []menuItem{{label: "kill", verb: "kill"}}},
		},
	}
	if got := m.View(); strings.Contains(got, "SHOULD-NOT-APPEAR") {
		t.Errorf("next-step line must not show in a submenu\n%s", got)
	}
}
