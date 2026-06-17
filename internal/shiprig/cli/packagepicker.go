package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

var (
	pkgPickTitle = lipgloss.NewStyle().Bold(true).Foreground(brand.Paper)
	pkgPickDim   = lipgloss.NewStyle().Foreground(brand.Muted)
	pkgPickCur   = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentShip)
	pkgPickSel   = lipgloss.NewStyle().Foreground(brand.AccentShip)
	pkgPickExcl  = lipgloss.NewStyle().Strikethrough(true).Foreground(brand.Muted)
	pkgPickPriv  = lipgloss.NewStyle().Foreground(brand.Cyan)
)

// pkgPickRow is one package line in the picker. ignored is recomputed from the
// live ignore set on every rebuild, so a toggle updates the row's styling without
// re-discovering the workspace.
type pkgPickRow struct {
	name, eco, bump, next, cur string
	private                    bool
	ignored                    bool
}

// pkgPickerModel is the bubbletea include/exclude picker for release packages,
// modeled on rig's run picker: arrow-key navigation, `x`/`i` to exclude/include,
// `a` to reveal excluded rows. Each `x`/`i` writes the changeset config `ignore`
// list immediately (like the run picker live-edits .rig.json), so `enter`/`q`
// just closes.
type pkgPickerModel struct {
	all    []pkgPickRow // full package set, in display order
	vis    []pkgPickRow // currently visible (honors showIgnored)
	ignore []string     // live ignore globs — source of truth for the ignored flag

	cursor      int
	nameW, ecoW int
	showIgnored bool
	status      string
	cancelled   bool

	// write persists the ignore list; a seam so tests can toggle without touching
	// the filesystem. Defaults to commands.WriteIgnore.
	write func([]string) (path string, ok bool, err error)
}

func newPkgPicker(rps []commands.ReleasePkg, ignore []string) pkgPickerModel {
	m := pkgPickerModel{
		ignore: commands.NormalizeIgnore(ignore),
		write:  commands.WriteIgnore,
	}
	for _, p := range rps {
		m.all = append(m.all, pkgPickRow{
			name: p.Name, eco: p.Eco, bump: p.Bump, next: p.Next, cur: p.Current, private: p.Private,
		})
	}
	m.rebuild()
	return m
}

// rebuild recomputes each row's ignored flag from the live ignore set and the
// visible slice (hiding ignored rows unless showIgnored), plus column widths.
func (m *pkgPickerModel) rebuild() {
	cfg := &config.Config{Ignore: m.ignore}
	m.vis = m.vis[:0]
	m.nameW, m.ecoW = 0, 0
	for i := range m.all {
		m.all[i].ignored = cfg.IsIgnored(m.all[i].name)
		if m.all[i].ignored && !m.showIgnored {
			continue
		}
		m.vis = append(m.vis, m.all[i])
		m.nameW = maxInt(m.nameW, runeCount(m.all[i].name))
		m.ecoW = maxInt(m.ecoW, runeCount(m.all[i].eco))
	}
	if m.cursor >= len(m.vis) {
		m.cursor = maxInt(0, len(m.vis)-1)
	}
}

func (m pkgPickerModel) Init() tea.Cmd { return nil }

func (m pkgPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc", "enter":
		if key.String() == "ctrl+c" {
			m.cancelled = true
		}
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.vis)-1 {
			m.cursor++
		}
	case "a":
		m.showIgnored = !m.showIgnored
		m.rebuild()
	case "x":
		return m.exclude(), nil
	case "i":
		return m.include(), nil
	}
	return m, nil
}

// exclude adds the highlighted package's exact name to the ignore list and
// persists it. A no-op when the row is already ignored.
func (m pkgPickerModel) exclude() pkgPickerModel {
	r, ok := m.cursorRow()
	if !ok || r.ignored {
		return m
	}
	m.ignore = commands.NormalizeIgnore(append(m.ignore, r.name))
	return m.persist(r.name + " excluded")
}

// include re-includes the highlighted package by dropping every ignore entry
// that matches it — its exact name and any glob (e.g. "*-demo") that covers it.
func (m pkgPickerModel) include() pkgPickerModel {
	r, ok := m.cursorRow()
	if !ok {
		return m
	}
	next := removeIgnoreMatching(m.ignore, r.name)
	if len(next) == len(m.ignore) {
		return m // nothing matched — already included
	}
	m.ignore = commands.NormalizeIgnore(next)
	return m.persist(r.name + " included")
}

// persist writes the live ignore list and rebuilds, setting the status line.
func (m pkgPickerModel) persist(okMsg string) pkgPickerModel {
	_, ok, err := m.write(m.ignore)
	switch {
	case err != nil:
		m.status = "could not write config: " + err.Error()
	case !ok:
		m.status = "could not write config (refusing to clobber it)"
	default:
		m.status = okMsg + "  ·  saved to .changeset config"
	}
	m.rebuild()
	return m
}

func (m pkgPickerModel) cursorRow() (pkgPickRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.vis) {
		return pkgPickRow{}, false
	}
	return m.vis[m.cursor], true
}

// removeIgnoreMatching drops every ignore entry that matches name — the exact
// name, and any glob that covers it — so re-including a package also reveals the
// siblings of a directory/wildcard glob.
func removeIgnoreMatching(globs []string, name string) []string {
	var out []string
	for _, g := range globs {
		cfg := &config.Config{Ignore: []string{g}}
		if g == name || cfg.IsIgnored(name) {
			continue
		}
		out = append(out, g)
	}
	return out
}

func (m pkgPickerModel) View() string {
	var b strings.Builder
	b.WriteString(pkgPickTitle.Render("Packages to release"))
	if m.showIgnored {
		b.WriteString("   " + pkgPickDim.Render("(showing excluded)"))
	}
	b.WriteString("\n\n")

	if len(m.vis) == 0 {
		b.WriteString(pkgPickDim.Render("  (no packages)") + "\n")
	}
	for i, r := range m.vis {
		b.WriteString(m.renderRow(r, i == m.cursor) + "\n")
	}

	if m.status != "" {
		b.WriteString("\n" + pkgPickDim.Render("  "+m.status) + "\n")
	}
	b.WriteString("\n" + pkgPickDim.Render("  ↑/↓ move · x exclude · i include · a show/hide excluded · enter done · q quit") + "\n")
	return b.String()
}

func (m pkgPickerModel) renderRow(r pkgPickRow, selected bool) string {
	gutter, name := "    ", padRight(r.name, m.nameW)
	switch {
	case selected:
		gutter = "  " + pkgPickCur.Render("▸ ")
		name = pkgPickSel.Render(name)
	case r.ignored:
		name = pkgPickExcl.Render(name)
	}
	meta := pkgPickDim.Render(padRight(r.eco, m.ecoW))
	return gutter + name + "  " + meta + "  " + m.statusCol(r)
}

// statusCol renders the right-hand disposition: version move, private, or excluded.
func (m pkgPickerModel) statusCol(r pkgPickRow) string {
	switch {
	case r.ignored:
		return pkgPickDim.Render("excluded")
	case r.private && r.next != "":
		return fmt.Sprintf("%s %s→%s", pkgPickDim.Render(r.bump), pkgPickDim.Render(r.cur), r.next) +
			"  " + pkgPickPriv.Render("private (not published)")
	case r.private:
		return pkgPickPriv.Render("private (not published)")
	case r.next != "":
		return fmt.Sprintf("%s  %s→%s", pkgPickDim.Render(padRight(r.bump, 5)), pkgPickDim.Render(r.cur), r.next)
	default:
		return pkgPickDim.Render("no change")
	}
}

// RunPackagePicker discovers the release packages and runs the interactive
// include/exclude picker, persisting choices to the changeset config `ignore`
// list. It draws on stderr (keeping stdout clean) like the other rig pickers.
func RunPackagePicker(ctx context.Context, ws *commands.Workspace) error {
	rps, err := commands.ReleasePackages(ctx, ws)
	if err != nil {
		return err
	}
	m := newPkgPicker(rps, ws.Config.Ignore)
	_, err = tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr)).Run()
	return err
}

func runeCount(s string) int { return utf8.RuneCountInString(s) }

func padRight(s string, w int) string {
	if n := runeCount(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
