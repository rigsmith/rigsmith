package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/spf13/cobra"
)

// NewBrowseCmd builds `changerig browse` — an interactive browser/manager for
// the pending changesets: list them (with bump badge, packages, summary), open
// one to read its full body, and manage it (delete, or edit in $EDITOR). Off an
// interactive terminal it prints a plain list instead.
func NewBrowseCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "browse",
		Aliases: []string{"ls", "list"},
		Short:   "Browse and manage pending changesets",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			items, err := loadChangesets(ws.ChangesetDir)
			if err != nil {
				return fmt.Errorf("reading changesets: %w", err)
			}
			if !browseInteractive() {
				printChangesetList(cmd.OutOrStdout(), items)
				return nil
			}
			_, err = tea.NewProgram(newBrowseModel(ws.ChangesetDir, items),
				tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout())).Run()
			return err
		},
	}
}

// browseInteractive reports whether the interactive browser should run.
func browseInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// loadChangesets reads the changeset dir and sorts by ID for a stable order.
func loadChangesets(dir string) ([]*changeset.Changeset, error) {
	items, err := changeset.Dir(dir, "")
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// printChangesetList is the non-interactive fallback: one line per changeset.
func printChangesetList(w io.Writer, items []*changeset.Changeset) {
	if len(items) == 0 {
		fmt.Fprintln(w, DimStyle.Render("No pending changesets."))
		return
	}
	for _, cs := range items {
		badge, style := changesetBadge(cs)
		fmt.Fprintf(w, "  %s %s  %s  %s\n",
			style.Render(fmt.Sprintf("%-6s", badge)), cs.ID,
			DimStyle.Render(strings.Join(cs.ChangedNames(), ", ")),
			DimStyle.Render(firstLine(cs.Summary)))
	}
}

// changesetBadge is the short bump/type tag for a changeset: the highest
// explicit release bump, else the conventional type (breaking → major styling),
// else "auto" (the planner derives the bump from the type at version time).
func changesetBadge(cs *changeset.Changeset) (string, lipgloss.Style) {
	hi := changeset.BumpNone
	for _, r := range cs.Releases {
		hi = hi.Max(r.Bump)
	}
	if hi != changeset.BumpNone {
		return hi.String(), styleFor(hi)
	}
	if typ, breaking, ok := cs.EffectiveType(); ok {
		if breaking {
			return typ + "!", MajorStyle
		}
		return typ, DimStyle
	}
	return "auto", DimStyle
}

// ---- the browser (bubbletea) -------------------------------------------

type browseModel struct {
	dir     string
	items   []*changeset.Changeset
	cursor  int
	detail  bool // false = list, true = body view
	confirm bool // a delete confirmation is pending
	vp      viewport.Model
	ready   bool
	w, h    int
	status  string // transient note (e.g. "deleted X")
}

func newBrowseModel(dir string, items []*changeset.Changeset) browseModel {
	return browseModel{dir: dir, items: items}
}

func (m browseModel) Init() tea.Cmd { return nil }

// editDoneMsg is sent after the external editor exits.
type editDoneMsg struct{ err error }

func (m browseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		vpH := msg.Height - 2
		if vpH < 3 {
			vpH = 3
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, vpH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = msg.Width, vpH
		}
		return m, nil

	case editDoneMsg:
		m.reload()
		return m, nil

	case tea.KeyMsg:
		m.status = ""
		if m.confirm {
			return m.updateConfirm(msg)
		}
		if m.detail {
			return m.updateDetail(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m browseModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.deleteCurrent()
		m.confirm = false
	default: // n / esc / anything else cancels
		m.confirm = false
	}
	return m, nil
}

func (m browseModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left", "backspace":
		m.detail = false
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	case "d":
		if len(m.items) > 0 {
			m.confirm = true
		}
		return m, nil
	case "e":
		return m, m.editCurrent()
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m browseModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.items) - 1
	case "enter", "l", "right":
		if len(m.items) > 0 {
			m.openDetail()
		}
	case "d":
		if len(m.items) > 0 {
			m.confirm = true
		}
	case "e":
		if len(m.items) > 0 {
			return m, m.editCurrent()
		}
	}
	return m, nil
}

func (m *browseModel) openDetail() {
	m.vp.SetContent(renderChangesetBody(m.items[m.cursor]))
	m.vp.GotoTop()
	m.detail = true
}

// currentPath is the on-disk file for the selected changeset (ID + ".md").
func (m browseModel) currentPath() string {
	return filepath.Join(m.dir, m.items[m.cursor].ID+".md")
}

// editCurrent opens the selected changeset in $EDITOR, suspending the UI.
func (m browseModel) editCurrent() tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, m.currentPath()) //nolint:gosec // editor from env, path from disk
	return tea.ExecProcess(c, func(err error) tea.Msg { return editDoneMsg{err} })
}

// deleteCurrent removes the selected changeset file and refreshes the list.
func (m *browseModel) deleteCurrent() {
	id := m.items[m.cursor].ID
	if err := os.Remove(m.currentPath()); err != nil {
		m.status = "could not delete " + id + ": " + err.Error()
		return
	}
	m.reload()
	m.status = "deleted " + id
}

// reload re-reads the changeset dir and clamps the cursor / exits the detail
// view if the list shrank.
func (m *browseModel) reload() {
	if items, err := loadChangesets(m.dir); err == nil {
		m.items = items
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if len(m.items) == 0 {
		m.detail = false
	}
}

func (m browseModel) View() string {
	if !m.ready {
		return "loading…"
	}
	if len(m.items) == 0 {
		return menuTitle.Render("Changesets") + "\n\n" +
			DimStyle.Render("No pending changesets — `changerig add` to create one.") + "\n\n" +
			DimStyle.Render("q quit")
	}
	if m.detail {
		return m.detailView()
	}
	return m.listView()
}

func (m browseModel) listView() string {
	var b strings.Builder
	b.WriteString(menuTitle.Render(fmt.Sprintf("Changesets (%d)", len(m.items))) + "\n")
	for i, cs := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = menuCursor.Render("▸ ")
		}
		badge, style := changesetBadge(cs)
		id := cs.ID
		if i == m.cursor {
			id = menuSelected.Render(id)
		}
		pkgs := strings.Join(cs.ChangedNames(), ", ")
		fmt.Fprintf(&b, "%s%s %s  %s\n", cursor, style.Render(fmt.Sprintf("%-6s", badge)), id,
			DimStyle.Render(truncate(pkgs+"  "+firstLine(cs.Summary), max(10, m.w-len(cs.ID)-12))))
	}
	footer := "↑/↓ move · enter view · d delete · e edit · q quit"
	if m.status != "" {
		footer = m.status + "  ·  " + footer
	}
	b.WriteString("\n" + DimStyle.Render(footer))
	return b.String()
}

func (m browseModel) detailView() string {
	cs := m.items[m.cursor]
	badge, style := changesetBadge(cs)
	header := style.Render(badge) + "  " + menuTitle.Render(cs.ID)
	footer := "↑/↓ scroll · e edit · d delete · esc back · q quit"
	if m.confirm {
		footer = MajorStyle.Render("delete " + cs.ID + "? (y/n)")
	}
	return header + "\n" + m.vp.View() + "\n" + DimStyle.Render(footer)
}

// renderChangesetBody renders a changeset's releases, type, and full summary.
func renderChangesetBody(cs *changeset.Changeset) string {
	var b strings.Builder
	b.WriteString(HeaderStyle.Render("Releases") + "\n")
	if len(cs.Releases) == 0 {
		b.WriteString(DimStyle.Render("  (none — empty changeset)") + "\n")
	}
	for _, r := range cs.Releases {
		bump := r.Bump.String()
		if r.Bump == changeset.BumpNone {
			bump = DimStyle.Render("auto")
		} else {
			bump = styleFor(r.Bump).Render(bump)
		}
		fmt.Fprintf(&b, "  %s  %s\n", r.Name, bump)
	}
	if typ, breaking, ok := cs.EffectiveType(); ok {
		label := typ
		if breaking {
			label += "!  (breaking)"
		}
		b.WriteString("\n" + HeaderStyle.Render("Type") + "  " + label + "\n")
	}
	b.WriteString("\n" + HeaderStyle.Render("Summary") + "\n")
	summary := strings.TrimRight(cs.Summary, "\n")
	if summary == "" {
		summary = DimStyle.Render("  (no summary)")
	}
	b.WriteString(summary + "\n")
	return b.String()
}

// truncate shortens s to max runes with an ellipsis.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max || max < 1 {
		return s
	}
	return string(r[:max-1]) + "…"
}
