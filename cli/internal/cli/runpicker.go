package cli

import (
	"os"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// runChoice is the outcome of the `run` picker: a cancel, or an index into the
// packages (script=false) or scripts (script=true) it was built from.
type runChoice struct {
	cancel bool
	script bool
	index  int
}

// runPickRow is one selectable line: name · ecosystem · path.
type runPickRow struct {
	name, eco, path string
	script          bool // false → packages slice, true → scripts slice
	index           int
}

// runPickSection is a titled group of rows ("Projects", "Scripts").
type runPickSection struct {
	title string
	rows  []runPickRow
}

// pickRunTarget shows the grouped run picker — runnable packages under
// "Projects", surfaced scripts under "Scripts" — and returns the chosen target.
// The name/ecosystem/path columns are aligned across every row so the two groups
// read as one table.
func pickRunTarget(tasks []allTask, scripts []scriptEntry) runChoice {
	var sections []runPickSection

	var projRows []runPickRow
	for i, t := range tasks {
		projRows = append(projRows, runPickRow{name: t.name, eco: t.eco, path: taskPath(t), index: i})
	}
	if len(projRows) > 0 {
		sections = append(sections, runPickSection{title: "Projects", rows: projRows})
	}

	var scriptRows []runPickRow
	for i, s := range scripts {
		path := s.loc
		if path == "" {
			path = "."
		}
		scriptRows = append(scriptRows, runPickRow{name: s.name, eco: s.eco, path: path, script: true, index: i})
	}
	if len(scriptRows) > 0 {
		sections = append(sections, runPickSection{title: "Scripts", rows: scriptRows})
	}

	// Draw on stderr (where interactive() verified a TTY), keeping stdout clean
	// for the command the picker then runs — matching the huh pickers.
	res, err := tea.NewProgram(newRunPickerModel(sections),
		tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return runChoice{cancel: true}
	}
	m := res.(runPickerModel)
	if m.cancelled || m.cursor < 0 || m.cursor >= len(m.flat) {
		return runChoice{cancel: true}
	}
	r := m.flat[m.cursor]
	return runChoice{script: r.script, index: r.index}
}

type runPickerModel struct {
	sections  []runPickSection
	flat      []runPickRow // selectable rows in display order (parallel to the sections)
	cursor    int
	nameW     int
	ecoW      int
	cancelled bool
}

func newRunPickerModel(sections []runPickSection) runPickerModel {
	var flat []runPickRow
	nameW, ecoW := 0, 0
	for _, s := range sections {
		for _, r := range s.rows {
			flat = append(flat, r)
			nameW = max(nameW, runeLen(r.name))
			ecoW = max(ecoW, runeLen(r.eco))
		}
	}
	return runPickerModel{sections: sections, flat: flat, nameW: nameW, ecoW: ecoW}
}

func (m runPickerModel) Init() tea.Cmd { return nil }

func (m runPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
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
	case "enter", "right", "l":
		return m, tea.Quit
	}
	return m, nil
}

var runPickHeader = lipgloss.NewStyle().Bold(true).Foreground(brandMuted)

func (m runPickerModel) View() string {
	var b strings.Builder
	b.WriteString(menuTitle.Render("Run which?") + "\n\n")
	idx := 0
	for si, s := range m.sections {
		if si > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  " + runPickHeader.Render(s.title) + "\n")
		for range s.rows {
			r := m.flat[idx]
			cursor, name := "    ", padRight(r.name, m.nameW)
			if idx == m.cursor {
				cursor = "  " + menuCursor.Render("▸ ")
				name = menuSelected.Render(name)
			}
			meta := dimStyle.Render(padRight(r.eco, m.ecoW) + "  " + r.path)
			b.WriteString(cursor + name + "  " + meta + "\n")
			idx++
		}
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · enter select · q quit") + "\n")
	return b.String()
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
