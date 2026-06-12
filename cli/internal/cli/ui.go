package cli

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newUICmd builds the `ui` command — a grouped bubbletea menu over rig's verbs.
// Everyday verbs sit at the top; the longer tail lives under `▸` sub-menus.
// Selecting a verb runs its command for the detected ecosystem.
func newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive menu",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			if _, err := resolvePrimary(cwd, detect.Root(cwd)); err != nil {
				return err
			}
			res, err := tea.NewProgram(newMenu()).Run()
			if err != nil {
				return err
			}
			final := res.(menuModel)
			if final.chosen == "" {
				return nil
			}
			return dispatch(cmd, final.chosen)
		},
	}
}

// dispatch runs the verb chosen in the menu, routing to the standalone commands
// where one exists, otherwise to the generic ecosystem verb.
func dispatch(cmd *cobra.Command, verb string) error {
	var sub *cobra.Command
	switch verb {
	case "coverage":
		sub = newCoverageCmd()
	case "doctor":
		sub = newDoctorCmd()
	case "kill":
		sub = newKillCmd()
	default:
		sub = verbCmd(verb, "")
	}
	sub.SetContext(cmd.Context())
	sub.SetOut(cmd.OutOrStdout())
	sub.SetErr(cmd.ErrOrStderr())
	return sub.RunE(sub, nil)
}

// menuItem is either an action (verb set) or a group (children set).
type menuItem struct {
	label    string
	desc     string
	verb     string
	children []menuItem
}

type frame struct {
	title  string
	items  []menuItem
	cursor int
}

type menuModel struct {
	header string
	stack  []frame // stack[len-1] is the visible level
	chosen string
}

var (
	menuTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	menuCursor   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

func newMenu() menuModel {
	cwd, _ := os.Getwd()
	root := detect.Root(cwd)
	eco, err := resolvePrimary(cwd, root)
	primary := eco
	if err != nil {
		primary = err.Error()
		eco = ""
	}

	// Capabilities probing: only show verbs the primary ecosystem actually maps.
	// Verbs handled by dedicated commands (coverage/kill/doctor) always apply.
	maps := func(verb string) bool {
		if eco == "" {
			return true
		}
		_, ok := detect.CommandFor(eco, verb, root)
		return ok
	}
	dev := keepMapped(maps, []menuItem{
		{label: "build", desc: "build the project", verb: "build"},
		{label: "test", desc: "run the tests", verb: "test"},
		{label: "run", desc: "run the project", verb: "run"},
		{label: "format", desc: "format the code", verb: "format"},
		{label: "lint", desc: "lint the code", verb: "lint"},
		{label: "typecheck", desc: "type-check the code", verb: "typecheck"},
	})
	deps := keepMapped(maps, []menuItem{
		{label: "install", desc: "install/restore deps", verb: "install"},
		{label: "ci", desc: "frozen/clean install", verb: "ci"},
		{label: "outdated", desc: "list outdated deps", verb: "outdated"},
		{label: "upgrade", desc: "upgrade deps", verb: "upgrade"},
	})
	maint := append(keepMapped(maps, []menuItem{{label: "clean", desc: "remove build outputs", verb: "clean"}}),
		menuItem{label: "coverage", desc: "tests + coverage", verb: "coverage"},
		menuItem{label: "kill", desc: "terminate app processes", verb: "kill"},
		menuItem{label: "doctor", desc: "check the environment", verb: "doctor"},
	)

	top := dev
	if len(deps) > 0 {
		top = append(top, menuItem{label: "▸ Dependencies", desc: "install / outdated / upgrade …", children: deps})
	}
	top = append(top, menuItem{label: "▸ Maintenance", desc: "clean / coverage / kill / doctor", children: maint})

	return menuModel{
		header: fmt.Sprintf("%s  ·  %s", root, primary),
		stack:  []frame{{title: "", items: top}},
	}
}

// keepMapped filters menu items to those whose verb the ecosystem maps.
func keepMapped(maps func(string) bool, items []menuItem) []menuItem {
	out := items[:0]
	for _, it := range items {
		if maps(it.verb) {
			out = append(out, it)
		}
	}
	return out
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m *menuModel) top() *frame { return &m.stack[len(m.stack)-1] }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	cur := m.top()
	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "backspace", "left", "h":
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		if cur.cursor > 0 {
			cur.cursor--
		}
	case "down", "j":
		if cur.cursor < len(cur.items)-1 {
			cur.cursor++
		}
	case "enter", "right", "l":
		it := cur.items[cur.cursor]
		if len(it.children) > 0 {
			m.stack = append(m.stack, frame{title: it.label, items: it.children})
			return m, nil
		}
		m.chosen = it.verb
		return m, tea.Quit
	}
	return m, nil
}

func (m menuModel) View() string {
	cur := m.stack[len(m.stack)-1]
	var b strings.Builder
	crumb := "rig"
	if cur.title != "" {
		crumb += dimStyle.Render(" / ") + strings.TrimPrefix(cur.title, "▸ ")
	}
	b.WriteString(menuTitle.Render(crumb) + "  " + dimStyle.Render(m.header) + "\n\n")
	for i, it := range cur.items {
		cursor := "  "
		label := it.label
		if i == cur.cursor {
			cursor = menuCursor.Render("▸ ")
			label = menuSelected.Render(label)
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, label, dimStyle.Render(it.desc)))
	}
	hint := "↑/↓ move · enter select · q quit"
	if len(m.stack) > 1 {
		hint = "↑/↓ move · enter select · esc back · q quit"
	}
	b.WriteString("\n" + dimStyle.Render(hint) + "\n")
	return b.String()
}
