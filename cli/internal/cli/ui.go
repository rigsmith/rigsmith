package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
			// A custom/script verb carries its own prebuilt command; run it directly.
			if final.chosenCmd != nil {
				sub := final.chosenCmd
				sub.SetContext(cmd.Context())
				sub.SetOut(cmd.OutOrStdout())
				sub.SetErr(cmd.ErrOrStderr())
				return sub.RunE(sub, nil)
			}
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
	case verb == "init":
		sub = newRigInitCmd()
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
	label       string
	desc        string
	verb        string
	cmd         *cobra.Command // a prebuilt command to run (custom/script verbs); preferred over verb
	children    []menuItem
	recommended bool   // marked as the suggested next step
	pickFocus   bool   // opens the project picker
	focusName   string // selecting focuses this project
	clearFocus  bool   // selecting returns to the whole repo
}

type frame struct {
	title  string
	items  []menuItem
	cursor int
}

type menuModel struct {
	header    string
	nextStep  string         // context-aware "next step" line shown at the top level
	stack     []frame        // stack[len-1] is the visible level
	chosen    string         // the verb selected on exit
	chosenCmd *cobra.Command // a prebuilt command selected on exit (custom/script verb)
	focus     string         // the focused project ("" = whole repo); scopes verbs
	projects  []string       // picker candidates (shown when more than one exists)
}

var (
	menuTitle    = lipgloss.NewStyle().Bold(true).Foreground(brandViolet)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(brandCyan)
	menuCursor   = lipgloss.NewStyle().Foreground(brandCyan)
	menuNext     = lipgloss.NewStyle().Bold(true).Foreground(brandGreen)
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
	// No `.rig.json` yet → lead with init: it pins the ecosystem (so a polyglot
	// repo stops asking) and is where custom verbs live. The next step in view.
	var nextStep string
	if _, err := os.Stat(filepath.Join(root, config.FileName)); os.IsNotExist(err) {
		top = append(top, menuItem{label: "init", desc: "scaffold .rig.json (pin conventions, add verbs)", verb: "init", recommended: true})
		nextStep = "No " + config.FileName + " yet — init pins conventions and adds custom verbs."
	}
	if len(projects) > 1 {
		top = append(top, menuItem{pickFocus: true, desc: "scope verbs to one project"})
	}
	top = append(top, dev...)
	if len(deps) > 0 {
		top = append(top, menuItem{label: "▸ Dependencies", desc: "install / outdated / upgrade …", children: deps})
	}
	// Surface this repo's *configured* commands — `.rig.json` custom commands and
	// discovered scripts (package.json, Go scripts/*/cmd) — so the menu reflects
	// what the repo actually offers, not just the built-in verbs.
	if proj := projectCommandItems(root); len(proj) > 0 {
		top = append(top, menuItem{label: "▸ Project commands", desc: "custom commands + scripts from this repo", children: proj})
	}
	top = append(top, menuItem{label: "▸ Maintenance", desc: "clean / coverage / kill / doctor", children: maint})

	return menuModel{
		header:   fmt.Sprintf("%s  ·  %s", root, primary),
		nextStep: nextStep,
		stack:    []frame{{title: "", items: top}},
		projects: projects,
	}
}

// projectCommandItems gathers the repo's configured commands — `.rig.json`
// custom commands plus discovered scripts (package.json, Go scripts/*/cmd) — as
// menu items that carry their own prebuilt command. Names are deduped with the
// same precedence the CLI uses (custom > package.json > Go script verbs), so the
// menu mirrors `rig <name>`.
func projectCommandItems(root string) []menuItem {
	var cmds []*cobra.Command
	if cfg, err := config.LoadMerged(root); err == nil {
		cmds = append(cmds, customCmds(cfg)...)
	}
	cmds = append(cmds, scriptCmds(root)...)
	cmds = append(cmds, goScriptCmds(root)...)

	seen := map[string]bool{}
	var items []menuItem
	for _, c := range cmds {
		name := c.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, menuItem{label: name, desc: c.Short, cmd: c})
	}
	return items
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
		case it.cmd != nil:
			m.chosenCmd = it.cmd
			return m, tea.Quit
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
	// A chosen verb / command means we're quitting to run it — erase the menu so
	// the command's output starts clean instead of below a stale menu. (Plain
	// quit, with nothing chosen, keeps the menu in scrollback.)
	if m.chosen != "" || m.chosenCmd != nil {
		return ""
	}
	cur := m.stack[len(m.stack)-1]
	var b strings.Builder
	crumb := "rig"
	if m.focus != "" {
		crumb += dimStyle.Render(" · ") + menuSelected.Render(m.focus)
	}
	if cur.title != "" {
		crumb += dimStyle.Render(" / ") + strings.TrimPrefix(cur.title, "▸ ")
	}
	b.WriteString(menuTitle.Render(crumb) + "  " + dimStyle.Render(m.header) + "\n")
	// The next-step line only belongs on the top level (it's about the repo, not
	// a submenu); deeper frames keep the bare header.
	if m.nextStep != "" && len(m.stack) == 1 {
		b.WriteString(menuNext.Render("  → ") + dimStyle.Render(m.nextStep) + "\n")
	}
	b.WriteString("\n")
	for i, it := range cur.items {
		cursor := "  "
		label := m.itemLabel(it)
		if i == cur.cursor {
			cursor = menuCursor.Render("▸ ")
			label = menuSelected.Render(label)
		}
		if it.recommended {
			label += "  " + menuNext.Render("next")
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
