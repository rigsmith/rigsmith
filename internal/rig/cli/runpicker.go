package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
)

// runChoice is the outcome of the `run` picker: a cancel, or the chosen project
// (task) / script. The picker resolves the selection itself — it owns the live
// project list — so callers run the result directly rather than indexing.
type runChoice struct {
	cancel bool
	task   *allTask
	script *scriptEntry
}

// runPickRow is one selectable line: name · ecosystem · path. In the live picker
// each row carries its resolved target (task) or script and whether the current
// .rig.json excludes it; the legacy script/index fields back the static tests.
type runPickRow struct {
	name, eco, path string
	script          bool // false → packages slice, true → scripts slice
	index           int
	excluded        bool
	task            *allTask
	scr             *scriptEntry
}

// runPickSection is a titled group of rows ("Projects", "Scripts").
type runPickSection struct {
	title string
	rows  []runPickRow
}

// pickRunTarget shows the grouped run picker — runnable packages under
// "Projects", surfaced scripts under "Scripts" — and returns the chosen target.
// It owns the project list (rebuilt from runTargetEntries) so it can reveal
// excluded rows and live-edit .rig.json `exclude`; scripts are passed in and
// deduped out of Projects. ctx/root drive (re)discovery and config writes.
func pickRunTarget(ctx context.Context, root string, scripts []scriptEntry) runChoice {
	m := newRunPickerLive(ctx, root, scripts)
	// Draw on stderr (where interactive() verified a TTY), keeping stdout clean
	// for the command the picker then runs — matching the huh pickers.
	res, err := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return runChoice{cancel: true}
	}
	fm, ok := res.(runPickerModel)
	if !ok || fm.cancelled || fm.chosen == nil {
		return runChoice{cancel: true}
	}
	return *fm.chosen
}

type runPickerModel struct {
	sections  []runPickSection
	flat      []runPickRow // selectable rows in display order (parallel to the sections)
	cursor    int
	nameW     int
	ecoW      int
	cancelled bool

	// Live mode (set by newRunPickerLive): the picker manages the project list
	// and can edit .rig.json. Zero in the static constructor used by tests.
	ctx          context.Context
	root         string
	scripts      []scriptEntry
	entries      []runEntry
	defaultName  string // configured defaultProject, marked in the list and toggled with `d`
	showExcluded bool
	sort         sortMode
	filtering    bool
	query        string
	status       string
	pending      *pendingExclude
	chosen       *runChoice
}

// pendingExclude is the "just this project or the whole directory?" prompt shown
// when excluding a project that sits in a directory crowded with siblings.
type pendingExclude struct {
	name    string
	just    string // precise glob for this one project
	dirGlob string // "<dir>/*"
	dir     string
	n       int
}

func newRunPickerModel(sections []runPickSection) runPickerModel {
	m := runPickerModel{sections: sections}
	m.reflow()
	return m
}

// newRunPickerLive builds the project-managing picker. It discovers the run
// targets itself (so it can show excluded ones) and dedups the passed scripts
// out of Projects.
func newRunPickerLive(ctx context.Context, root string, scripts []scriptEntry) runPickerModel {
	m := runPickerModel{ctx: ctx, root: root, scripts: scripts, defaultName: defaultProjectFor(root)}
	m.entries = runTargetEntries(ctx, root)
	m.rebuild()
	return m
}

// reflow recomputes the flat row list and column widths from sections.
func (m *runPickerModel) reflow() {
	m.flat = nil
	m.nameW, m.ecoW = 0, 0
	for _, s := range m.sections {
		for _, r := range s.rows {
			m.flat = append(m.flat, r)
			m.nameW = max(m.nameW, runeLen(r.name))
			m.ecoW = max(m.ecoW, runeLen(r.eco))
		}
	}
	if m.cursor >= len(m.flat) {
		m.cursor = max(0, len(m.flat)-1)
	}
}

// rebuild regenerates the sections from the live entries + scripts, honoring the
// show-excluded toggle and deduping Go script verbs out of Projects.
func (m *runPickerModel) rebuild() {
	scriptDir := map[string]bool{}
	for _, s := range m.scripts {
		if s.eco == "go" {
			scriptDir[s.loc] = true
		}
	}

	var proj []runPickRow
	for _, e := range m.entries {
		rel := relSlash(m.root, e.t.Dir)
		if e.t.Eco == detect.Go && scriptDir[rel] {
			continue // surfaced as a script below
		}
		if !isRunnable(e.t) {
			continue
		}
		if e.excluded && !m.showExcluded {
			continue
		}
		if !nameMatches(m.query, e.t.Name) {
			continue
		}
		task, ok := runEntryTask(e, m.root)
		if !ok {
			continue
		}
		t := task
		proj = append(proj, runPickRow{name: e.t.Name, eco: e.t.Eco, path: taskPath(task), excluded: e.excluded, task: &t})
	}

	var scriptRows []runPickRow
	for i := range m.scripts {
		s := &m.scripts[i]
		if !nameMatches(m.query, s.name) {
			continue
		}
		path := s.loc
		if path == "" {
			path = "."
		}
		scriptRows = append(scriptRows, runPickRow{name: s.name, eco: s.eco, path: path, script: true, scr: s})
	}

	m.sortRows(proj)
	m.sortRows(scriptRows)

	var sections []runPickSection
	if len(proj) > 0 {
		sections = append(sections, runPickSection{title: "Projects", rows: proj})
	}
	if len(scriptRows) > 0 {
		sections = append(sections, runPickSection{title: "Scripts", rows: scriptRows})
	}
	m.sections = sections
	m.reflow()
}

// sortRows orders rows by the current sort mode (path by default, ecosystem on
// toggle).
func (m *runPickerModel) sortRows(rows []runPickRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rowLess(m.sort, rows[i].eco, rows[i].path, rows[i].name, rows[j].eco, rows[j].path, rows[j].name)
	})
}

// refresh re-discovers entries after a config change so excluded markers update.
func (m *runPickerModel) refresh() {
	m.entries = runTargetEntries(m.ctx, m.root)
	m.rebuild()
}

func (m runPickerModel) Init() tea.Cmd { return nil }

func (m runPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.pending != nil {
		return m.updatePending(key)
	}
	if m.filtering {
		return m.updateFilter(key)
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		m.cancelled = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.flat)-1 {
			m.cursor++
		}
	case "/":
		if m.live() {
			m.filtering = true
			m.status = ""
		}
	case "e":
		if m.live() {
			m.sort = m.sort.toggle()
			m.rebuild()
			m.status = "sorted by " + m.sort.String()
		}
	case "a":
		if m.live() {
			m.showExcluded = !m.showExcluded
			m.rebuild()
		}
	case "x":
		if m.live() {
			return m.excludeAtCursor()
		}
	case "i":
		if m.live() {
			return m.includeAtCursor()
		}
	case "d":
		if m.live() {
			return m.setDefaultAtCursor()
		}
	case "enter", "right", "l":
		if m.live() {
			m.choose()
		}
		return m, tea.Quit
	}
	return m, nil
}

// updateFilter handles keys while the `/` name filter is focused: runes edit the
// query (the list narrows live), arrows still move, enter runs the highlighted
// row, esc clears and leaves filtering.
func (m runPickerModel) updateFilter(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.filtering, m.query = false, ""
		m.rebuild()
	case tea.KeyEnter:
		m.filtering = false
		m.choose()
		return m, tea.Quit
	case tea.KeyCtrlC:
		m.cancelled = true
		return m, tea.Quit
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < len(m.flat)-1 {
			m.cursor++
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if m.query != "" {
			m.query = m.query[:len(m.query)-1]
			m.cursor = 0
			m.rebuild()
		}
	case tea.KeySpace:
		m.query += " "
		m.cursor = 0
		m.rebuild()
	case tea.KeyRunes:
		m.query += string(key.Runes)
		m.cursor = 0
		m.rebuild()
	}
	return m, nil
}

func (m *runPickerModel) live() bool { return m.root != "" }

// choose records the selection at the cursor for the live picker's caller.
func (m *runPickerModel) choose() {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return
	}
	r := m.flat[m.cursor]
	switch {
	case r.scr != nil:
		m.chosen = &runChoice{script: r.scr}
	case r.task != nil:
		m.chosen = &runChoice{task: r.task}
	}
}

// excludeAtCursor excludes the highlighted project, opening the whole-directory
// prompt first when it sits in a crowded directory.
func (m runPickerModel) excludeAtCursor() (tea.Model, tea.Cmd) {
	r, ok := m.projectAtCursor()
	if !ok {
		m.status = "exclude applies to projects, not scripts"
		return m, nil
	}
	glob := preciseExcludeGlob(r.name, r.path, m.projectNames())
	if dirGlob, dir, n, crowded := crowdedExcludeDir(r.path, m.projectRels()); crowded {
		m.pending = &pendingExclude{name: r.name, just: glob, dirGlob: dirGlob, dir: dir, n: n}
		return m, nil
	}
	m.applyExclude(glob)
	return m, nil
}

func (m runPickerModel) updatePending(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pending
	switch key.String() {
	case "esc", "q", "ctrl+c":
		m.pending = nil
		m.status = "cancelled"
	case "j": // just this project
		m.pending = nil
		m.applyExclude(p.just)
	case "d": // the whole directory
		m.pending = nil
		m.applyExclude(p.dirGlob)
	}
	return m, nil
}

// applyExclude writes glob to .rig.json, refreshes, and sets the status line.
func (m *runPickerModel) applyExclude(glob string) {
	status, ok := addExclude(m.root, glob)
	if ok {
		m.refresh()
		status += "  (a: show/hide excluded)"
	}
	m.status = status
}

// includeAtCursor re-includes the highlighted project by dropping every exclude
// glob that matches it (a directory glob reveals its siblings too).
func (m runPickerModel) includeAtCursor() (tea.Model, tea.Cmd) {
	r, ok := m.projectAtCursor()
	if !ok {
		m.status = "include applies to projects, not scripts"
		return m, nil
	}
	status, ok := removeExcludes(m.root, r.name, shortName(r.name), r.path)
	if ok {
		m.refresh()
	}
	m.status = status
	return m, nil
}

// setDefaultAtCursor makes the highlighted project the run default — writing
// `defaultProject` to .rig.json so a bare `rig run` launches it without the
// picker — or clears the default when the highlighted project already is it
// (a toggle). Scripts can't be a default project, so it no-ops with a status.
func (m runPickerModel) setDefaultAtCursor() (tea.Model, tea.Cmd) {
	r, ok := m.projectAtCursor()
	if !ok {
		m.status = "default applies to projects, not scripts"
		return m, nil
	}
	if defaultMatches(m.defaultName, r.name) {
		status, ok := clearRunDefault(m.root)
		if ok {
			m.defaultName = ""
		}
		m.status = status
		return m, nil
	}
	status, ok := setRunDefault(m.root, r.name)
	if ok {
		m.defaultName = r.name
	}
	m.status = status
	return m, nil
}

func (m runPickerModel) projectAtCursor() (runPickRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return runPickRow{}, false
	}
	r := m.flat[m.cursor]
	if r.script {
		return runPickRow{}, false
	}
	return r, true
}

func (m runPickerModel) projectRels() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, relSlash(m.root, e.t.Dir))
	}
	return out
}

func (m runPickerModel) projectNames() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.t.Name)
	}
	return out
}

var (
	runPickHeader  = lipgloss.NewStyle().Bold(true).Foreground(brandMuted)
	runPickExcl    = lipgloss.NewStyle().Strikethrough(true).Foreground(brandMuted)
	runPickDefault = lipgloss.NewStyle().Foreground(brandGreen)
)

func (m runPickerModel) View() string {
	var b strings.Builder

	// Title line carries the live sort / filter state.
	b.WriteString(menuTitle.Render("Run which?"))
	switch {
	case m.filtering:
		b.WriteString("   " + menuSelected.Render("/"+m.query+"▏"))
	case m.query != "":
		b.WriteString("   " + dimStyle.Render("filter: "+m.query))
	case m.live():
		b.WriteString("   " + dimStyle.Render("sort: "+m.sort.String()))
	}
	b.WriteString("\n\n")

	if len(m.flat) == 0 {
		b.WriteString(dimStyle.Render("  (no matching projects)") + "\n")
	}

	idx := 0
	for si, s := range m.sections {
		if si > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  " + runPickHeader.Render(s.title) + "\n")
		for range s.rows {
			b.WriteString(m.renderRow(m.flat[idx], idx == m.cursor) + "\n")
			idx++
		}
	}

	if m.pending != nil {
		p := m.pending
		b.WriteString("\n" + menuNext.Render("  Exclude ") +
			fmt.Sprintf("%s — [j] just this, [d] all %d under %s/, [esc] cancel\n", p.name, p.n, p.dir))
	} else if m.status != "" {
		b.WriteString("\n" + dimStyle.Render("  "+m.status) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render(m.hint()) + "\n")
	return b.String()
}

// renderRow lays one row out as aligned name · eco · path columns, with the
// cursor marker and the excluded styling.
func (m runPickerModel) renderRow(r runPickRow, selected bool) string {
	gutter, name := "    ", padRight(r.name, m.nameW)
	switch {
	case selected:
		gutter = "  " + menuCursor.Render("▸ ")
		name = menuSelected.Render(name)
	case r.excluded:
		name = runPickExcl.Render(name)
	}
	meta := dimStyle.Render(padRight(r.eco, m.ecoW) + "  " + r.path)
	row := gutter + name + "  " + meta
	if !r.script && defaultMatches(m.defaultName, r.name) {
		row += "  " + runPickDefault.Render("★ default")
	}
	return row
}

func (m runPickerModel) hint() string {
	switch {
	case !m.live():
		return "↑/↓ move · enter select · q quit"
	case m.filtering:
		return "type to filter · ↑/↓ move · enter run · esc clear"
	default:
		return "enter run · / filter · d set default · e sort · x/i exclude/include · a show/hide excluded · q quit"
	}
}

// runEntryTask resolves a run entry to a runnable task (its `run` argv + display
// path). ok is false when the ecosystem has no run mapping.
func runEntryTask(e runEntry, root string) (allTask, bool) {
	argv, ok := devCommandFor(e.t, "run", root)
	if !ok {
		return allTask{}, false
	}
	return allTask{name: e.t.Name, eco: e.t.Eco, dir: e.t.Dir, rel: relSlash(root, e.t.Dir), argv: argv}, true
}

// taskPath is a package's display path: its repo-relative dir, or "." at the root.
func taskPath(t allTask) string {
	if t.rel == "" || t.rel == "." {
		return "."
	}
	return t.rel
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// padRight pads s with spaces to a width of w runes (no-op when already wider).
func padRight(s string, w int) string {
	if n := runeLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// pickColumns lays a row out as `name  eco  path`, the name and eco columns
// padded to a shared width so a flat picker lines up.
func pickColumns(name, eco, path string, nameW, ecoW int) string {
	return padRight(name, nameW) + "  " + padRight(eco, ecoW) + "  " + path
}
