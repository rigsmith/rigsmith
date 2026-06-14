package commands

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/prestate"
	"github.com/spf13/cobra"
)

// MenuItem is an extra entry a tool contributes to its `ui` menu — shiprig adds
// its release verbs (publish/tag/release) this way. Extras are appended after
// the changeset-lifecycle items and before Info, so the menu reflects the whole
// surface the invoking tool actually offers.
type MenuItem struct {
	Label string
	Desc  string
	Build func() *cobra.Command
}

// NewUICmd builds the `ui` command — a bubbletea interactive menu over the
// changeset lifecycle. The menu is state-driven: it shows only the verbs that
// apply to the workspace's current state (source mode, pending changesets,
// prerelease) plus any tool-contributed extras.
func NewUICmd(extra ...MenuItem) *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Interactive menu",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := newMenu(cmd.Context(), cmd.Root().Name(), extra)
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
	label       string
	desc        string
	recommended bool // marked as the suggested next step for the current state
	build       func() *cobra.Command
}

type menuModel struct {
	items   []menuItem
	cursor  int
	title   string // the invoking tool's name (rig/changerig/shiprig)
	header  string
	hint    string // the context-aware "next step" line shown under the header
	chosen  func() *cobra.Command
	quitMsg string
}

var (
	menuTitle    = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentChange)
	menuSelected = lipgloss.NewStyle().Bold(true).Foreground(brand.Cyan)
	menuCursor   = lipgloss.NewStyle().Foreground(brand.Cyan)
	menuNext     = lipgloss.NewStyle().Bold(true).Foreground(brand.Green)
)

// wsState is the workspace context the menu adapts to: what source mode is
// configured, how many changesets are pending, and whether prerelease is active.
type wsState struct {
	initialized    bool
	usesChangesets bool
	usesCommits    bool
	pending        int
	inPre          bool
	preTag         string
}

func newMenu(ctx context.Context, title string, extra []MenuItem) menuModel {
	ws, err := Open()
	if err != nil {
		// Couldn't resolve a workspace — offer the verbs anyway (each reports its
		// own setup error if chosen) rather than dead-ending on a blank menu.
		return menuModel{title: title, header: "rigsmith", items: buildItems(wsState{initialized: true, usesChangesets: true}, extra)}
	}

	st := wsState{
		initialized:    ws.Initialized(),
		usesChangesets: ws.Config.UsesChangesets(),
		usesCommits:    ws.Config.UsesCommits(),
	}
	if !st.initialized {
		return menuModel{
			title:  title,
			header: fmt.Sprintf("%s  ·  not set up", ws.Root),
			hint:   "Releases aren't set up here yet — start with Initialize.",
			items:  buildItems(st, extra),
		}
	}

	if cs, err := changeset.Dir(ws.ChangesetDir, ""); err == nil {
		st.pending = len(cs)
	}
	if pre, _ := prestate.Read(ws.ChangesetDir); pre != nil && pre.Mode == prestate.ModePre {
		st.inPre, st.preTag = true, pre.Tag
	}
	pkgs, _, _ := ws.Discover(ctx)

	items := buildItems(st, extra)
	target, hint := recommend(st)
	cursor := markRecommended(items, target)

	header := fmt.Sprintf("%s  ·  %d package(s)  ·  %d pending changeset(s)", ws.Root, len(pkgs), st.pending)
	if st.inPre {
		header += "  ·  prerelease " + st.preTag
	}
	return menuModel{title: title, header: header, hint: hint, items: items, cursor: cursor}
}

// buildItems assembles the menu for a given workspace state, showing only the
// verbs that currently apply: an uninitialized workspace gets just Initialize +
// Info; Add/Browse appear only in changeset mode (Browse only with pending
// changesets); Version only when there's something to release; tool extras and
// an Exit-prerelease entry slot in when relevant.
func buildItems(st wsState, extra []MenuItem) []menuItem {
	if !st.initialized {
		return []menuItem{
			{label: "Initialize", desc: "set up releases here (.changeset/ + source)", recommended: true, build: NewInitCmd},
			{label: "Info", desc: "config + discovered packages", build: NewInfoCmd},
		}
	}

	items := []menuItem{
		{label: "Status", desc: "show the pending release plan", build: statusVerbose},
	}
	if st.usesChangesets {
		items = append(items, menuItem{label: "Add changeset", desc: "describe a pending release", build: NewAddCmd})
		if st.pending > 0 {
			items = append(items, menuItem{label: "Browse changesets", desc: "view / delete / edit pending changesets", build: NewBrowseCmd})
		}
	}
	if st.pending > 0 || st.usesCommits {
		items = append(items, menuItem{label: "Version", desc: "bump versions + write changelogs", build: NewVersionCmd})
	}
	for _, e := range extra {
		items = append(items, menuItem{label: e.Label, desc: e.Desc, build: e.Build})
	}
	if st.inPre {
		items = append(items, menuItem{label: fmt.Sprintf("Exit prerelease (%s)", st.preTag), desc: "graduate to stable on the next Version", build: preExitCmd})
	}
	items = append(items, menuItem{label: "Info", desc: "config + discovered packages", build: NewInfoCmd})
	return items
}

// recommend picks the suggested next-step label (always one that buildItems
// included for the same state) and the hint line describing it.
func recommend(st wsState) (target, hint string) {
	target = "Status"
	switch {
	case st.inPre:
		hint = fmt.Sprintf("Prerelease mode (%s) — Status shows the plan; Exit then Version to graduate.", st.preTag)
	case st.pending > 0:
		hint = fmt.Sprintf("%d pending changeset(s) — review with Status, then Version.", st.pending)
	case st.usesCommits:
		hint = "Commit-driven releases — Status shows what would ship."
	default:
		target, hint = "Add changeset", "No pending changesets — record a change with Add changeset."
	}
	return target, hint
}

// markRecommended flags the item whose label matches target and returns its
// index (so the cursor lands on the suggested next step). Falls back to 0.
func markRecommended(items []menuItem, target string) (cursor int) {
	for i := range items {
		if items[i].label == target {
			items[i].recommended = true
			return i
		}
	}
	return 0
}

// statusVerbose builds the Status command with --verbose preset, so the menu's
// Status shows the changes driving each package.
func statusVerbose() *cobra.Command { return withFlag(NewStatusCmd(), "verbose") }

// preExitCmd wraps `pre exit` so the menu can run it without typed arguments —
// it only appears when a prerelease is active, so exit is the meaningful action.
func preExitCmd() *cobra.Command {
	base := NewPreCmd()
	return &cobra.Command{RunE: func(cmd *cobra.Command, _ []string) error { return base.RunE(cmd, []string{"exit"}) }}
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
	// A chosen verb means we're quitting to run it — erase the menu so the verb's
	// output starts clean instead of below a stale menu. (Plain quit keeps it.)
	if m.chosen != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(menuTitle.Render(m.title) + "  " + DimStyle.Render(m.header) + "\n")
	if m.hint != "" {
		b.WriteString(menuNext.Render("  → ") + DimStyle.Render(m.hint) + "\n")
	}
	b.WriteString("\n")
	for i, it := range m.items {
		cursor := "  "
		label := it.label
		if i == m.cursor {
			cursor = menuCursor.Render("▸ ")
			label = menuSelected.Render(label)
		}
		if it.recommended {
			label += "  " + menuNext.Render("next")
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", cursor, label, DimStyle.Render(it.desc)))
	}
	b.WriteString("\n" + DimStyle.Render("↑/↓ move · enter select · q quit") + "\n")
	return b.String()
}
