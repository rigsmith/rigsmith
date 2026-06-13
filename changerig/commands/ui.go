package commands

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/changeset"
	"github.com/spf13/cobra"
)

// NewUICmd builds the `ui` command — a bubbletea interactive menu over the
// changeset lifecycle. It is the first piece of the charm TUI; a richer
// dashboard (live plan, package picker) builds on this model.
func NewUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive menu",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := newMenu(cmd.Context())
			res, err := tea.NewProgram(m).Run()
			if err != nil {
				return err
			}
			final := res.(menuModel)
			if final.chosen == nil {
				return nil
			}
			sub := final.chosen()
			sub.SetContext(cmd.Context())
			sub.SetOut(cmd.OutOrStdout())
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, nil)
		},
	}
}

type menuItem struct {
	label string
	desc  string
	build func() *cobra.Command
}

type menuModel struct {
	items   []menuItem
	cursor  int
	header  string
	chosen  func() *cobra.Command
	quitMsg string
}

var (
	menuTitle    = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentChange)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(brand.Cyan)
	menuCursor   = lipgloss.NewStyle().Foreground(brand.Cyan)
)

func newMenu(ctx context.Context) menuModel {
	header := "rigsmith"
	if ws, err := Open(); err == nil {
		n := 0
		if cs, err := changeset.Dir(ws.ChangesetDir, ""); err == nil {
			n = len(cs)
		}
		pkgs, _, _ := ws.Discover(ctx)
		header = fmt.Sprintf("%s  ·  %d package(s)  ·  %d pending changeset(s)", ws.Root, len(pkgs), n)
	}
	return menuModel{
		header: header,
		items: []menuItem{
			{"Status", "show the pending release plan", func() *cobra.Command { return withFlag(NewStatusCmd(), "verbose") }},
			{"Add changeset", "describe a pending release", NewAddCmd},
			{"Browse changesets", "view / delete / edit pending changesets", NewBrowseCmd},
			{"Version", "bump versions + write changelogs", NewVersionCmd},
			{"Info", "config + discovered packages", NewInfoCmd},
		},
	}
}

// withFlag sets a boolean flag's default to true (used so the menu's Status runs verbose).
func withFlag(c *cobra.Command, name string) *cobra.Command {
	if f := c.Flags().Lookup(name); f != nil {
		_ = c.Flags().Set(name, "true")
	}
	return c
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		m.chosen = m.items[m.cursor].build
		return m, tea.Quit
	}
	return m, nil
}

func (m menuModel) View() string {
	var b strings.Builder
	b.WriteString(menuTitle.Render("relrig") + "  " + DimStyle.Render(m.header) + "\n\n")
	for i, it := range m.items {
		cursor := "  "
		label := it.label
		if i == m.cursor {
			cursor = menuCursor.Render("▸ ")
			label = menuSelected.Render(label)
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, label, DimStyle.Render(it.desc)))
	}
	b.WriteString("\n" + DimStyle.Render("↑/↓ move · enter select · q quit") + "\n")
	return b.String()
}
