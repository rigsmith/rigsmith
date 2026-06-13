package cli

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
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
			if _, err := resolvePrimary(cwd, resolveRoot(cwd)); err != nil {
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
			return dispatch(cmd, final.chosen, final.focus)
		},
	}
}

// devLoopVerbs are the verbs that accept a [project] selector (devVerbCmd), so
// a menu focus can scope them to one package.
var devLoopVerbs = map[string]bool{
	"build": true, "test": true, "run": true, "format": true,
	"lint": true, "typecheck": true, "clean": true, "rebuild": true,
}

// dispatch runs the verb chosen in the menu, routing to the standalone commands
// where one exists, otherwise to the generic ecosystem verb. A non-empty focus
// (the menu's project picker) scopes project-aware verbs to that package, the
// .NET rig Menu's project submenu / the Node menu's focus.
func dispatch(cmd *cobra.Command, verb, focus string) error {
	var sub *cobra.Command
	var args []string
	switch {
	case verb == "coverage":
		sub = newCoverageCmd()
	case verb == "doctor":
		sub = newDoctorCmd()
	case verb == "kill":
		sub = newKillCmd()
		if focus != "" {
			args = []string{focus}
		}
	case verb == "self-update":
		sub = newSelfUpdateCmd()
	case devLoopVerbs[verb]:
		sub = devVerbCmd(verb, "", false)
		if focus != "" {
			args = []string{focus}
		}
	default:
		sub = verbCmd(verb, "")
	}
	sub.SetContext(cmd.Context())
	sub.SetOut(cmd.OutOrStdout())
	sub.SetErr(cmd.ErrOrStderr())
	return sub.RunE(sub, args)
}

// menuItem is an action (verb set), a group (children set), or a focus
// control (pickFocus opens the project picker; focusName/clearFocus set or
// clear the focus from inside it).
type menuItem struct {
	label      string
	desc       string
	verb       string
	children   []menuItem
	pickFocus  bool   // opens the project picker
	focusName  string // selecting focuses this project
	clearFocus bool   // selecting returns to the whole repo
}

type frame struct {
	title  string
	items  []menuItem
	cursor int
}

type menuModel struct {
	header   string
	stack    []frame // stack[len-1] is the visible level
	chosen   string
	focus    string   // the focused project ("" = whole repo); scopes verbs
	projects []string // picker candidates (shown when more than one exists)
}

var (
	menuTitle    = lipgloss.NewStyle().Bold(true).Foreground(brandViolet)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(brandCyan)
	menuCursor   = lipgloss.NewStyle().Foreground(brandCyan)
)

func newMenu() menuModel {
	cwd, _ := os.Getwd()
	root := resolveRoot(cwd)
	eco, err := resolvePrimary(cwd, root)
	primary := eco
	if err != nil {
		primary = err.Error()
		eco = ""
	}

	// Capabilities probing: only show verbs the primary ecosystem actually maps,
	// and (for .NET) only verbs the repo's projects support — no test project →
	// no test/coverage, no runnable project → no run. Kill/doctor always apply.
	caps := detect.AllCapabilities
	if eco == detect.DotNet {
		cfg, _ := config.LoadMerged(root)
		caps = detect.ProbeCapabilities(root, "", cfg.Exclude)
	}
	maps := func(verb string) bool {
		if eco == "" {
			return true
		}
		if caps.Unavailable(verb) != "" {
			return false
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
	maint := keepMapped(maps, []menuItem{{label: "clean", desc: "remove build outputs", verb: "clean"}})
	if caps.Unavailable("coverage") == "" {
		maint = append(maint, menuItem{label: "coverage", desc: "tests + coverage", verb: "coverage"})
	}
	maint = append(maint,
		menuItem{label: "kill", desc: "terminate app processes", verb: "kill"},
		menuItem{label: "doctor", desc: "check the environment", verb: "doctor"},
	)

	maint = append(maint, menuItem{label: "self-update", desc: "update rig itself", verb: "self-update"})

	// Project focus (the .NET rig Menu's project submenu / the Node menu's
	// focus): with several projects, a picker entry scopes subsequent verbs.
	projects := discoveredPackageNames(root, excludeFor(root))
	var top []menuItem
	if len(projects) > 1 {
		top = append(top, menuItem{pickFocus: true, desc: "scope verbs to one project"})
	}
	top = append(top, dev...)
	if len(deps) > 0 {
		top = append(top, menuItem{label: "▸ Dependencies", desc: "install / outdated / upgrade …", children: deps})
	}
	top = append(top, menuItem{label: "▸ Maintenance", desc: "clean / coverage / kill / doctor", children: maint})

	return menuModel{
		header:   fmt.Sprintf("%s  ·  %s", root, primary),
		stack:    []frame{{title: "", items: top}},
		projects: projects,
	}
}

// focusPickerItems builds the project-picker frame: "(whole repo)" to clear
// the focus, then every project.
func focusPickerItems(projects []string) []menuItem {
	items := []menuItem{{label: "(whole repo)", desc: "all projects", clearFocus: true}}
	for _, p := range projects {
		items = append(items, menuItem{label: p, focusName: p})
	}
	return items
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
		switch {
		case it.pickFocus:
			m.stack = append(m.stack, frame{title: "▸ Project", items: focusPickerItems(m.projects)})
			return m, nil
		case it.focusName != "":
			m.focus = it.focusName
			m.stack = m.stack[:len(m.stack)-1] // back out of the picker
			return m, nil
		case it.clearFocus:
			m.focus = ""
			m.stack = m.stack[:len(m.stack)-1]
			return m, nil
		case len(it.children) > 0:
			m.stack = append(m.stack, frame{title: it.label, items: it.children})
			return m, nil
		}
		m.chosen = it.verb
		return m, tea.Quit
	}
	return m, nil
}

// itemLabel renders an item's label; the focus-picker entry shows the live
// focus ("project: <name>", or "(all)" when unfocused).
func (m menuModel) itemLabel(it menuItem) string {
	if it.pickFocus {
		if m.focus != "" {
			return "▸ project: " + m.focus
		}
		return "▸ project: (all)"
	}
	return it.label
}

func (m menuModel) View() string {
	cur := m.stack[len(m.stack)-1]
	var b strings.Builder
	crumb := "rig"
	if m.focus != "" {
		crumb += dimStyle.Render(" · ") + menuSelected.Render(m.focus)
	}
	if cur.title != "" {
		crumb += dimStyle.Render(" / ") + strings.TrimPrefix(cur.title, "▸ ")
	}
	b.WriteString(menuTitle.Render(crumb) + "  " + dimStyle.Render(m.header) + "\n\n")
	for i, it := range cur.items {
		cursor := "  "
		label := m.itemLabel(it)
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
