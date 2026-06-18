package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/devroute"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// wtAction is what the bare `<tool>-wt` menu resolved to.
type wtAction int

const (
	wtCancel wtAction = iota // esc/q — do nothing
	wtRun                    // run the selected worktree (its path goes to stdout)
	wtPin                    // pin the selected worktree as the dev route
	wtUnpin                  // clear the pinned route
)

var (
	wtCursorStyle   = lipgloss.NewStyle().Foreground(brand.Cyan)
	wtSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(brand.Cyan)
	wtPinStyle      = lipgloss.NewStyle().Foreground(brand.Green)
)

// wtMenuModel is the navigable worktree menu a bare `<tool>-wt` shows: pick a
// worktree and run it now (enter), or pin it as the active -dev route (p) / clear
// the pin (u). Following the dashboard pattern, it only records the decision; the
// command acts after tea exits, so no work runs inside the event loop.
type wtMenuModel struct {
	wts    []gitrepo.Worktree
	pinned string // currently-pinned worktree path ("" = none)
	cursor int
	action wtAction
	chosen string // selected worktree path (for run/pin)
}

// newWtMenu builds the menu with the worktrees ordered newest-first and the
// cursor parked on the pinned worktree, if any.
func newWtMenu(wts []gitrepo.Worktree, pinned string) wtMenuModel {
	m := wtMenuModel{wts: worktreesByRecent(wts), pinned: pinned}
	for i, wt := range m.wts {
		if pinned != "" && sameDir(wt.Path, pinned) {
			m.cursor = i
			break
		}
	}
	return m
}

func (m wtMenuModel) Init() tea.Cmd { return nil }

// Update drives the menu: ↑/↓ (k/j) move; enter runs the selected worktree; p
// pins it as the -dev route; u (or x) clears the pin; q/esc cancel.
func (m wtMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q", "esc", "ctrl+c":
		m.action = wtCancel
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.wts)-1 {
			m.cursor++
		}
	case "enter":
		m.action = wtRun
		m.chosen = m.wts[m.cursor].Path
		return m, tea.Quit
	case "p":
		m.action = wtPin
		m.chosen = m.wts[m.cursor].Path
		return m, tea.Quit
	case "u", "x":
		m.action = wtUnpin
		return m, tea.Quit
	}
	return m, nil
}

func (m wtMenuModel) View() string {
	var b strings.Builder
	b.WriteString(HeaderStyle.Render("worktrees") + "  " +
		DimStyle.Render("run one, or pin it as the -dev route") + "\n\n")

	// Pad the branch and age into fixed-width columns so the dim path column lines
	// up — matching the label + detail layout the other rig menus use.
	now := time.Now()
	branches := make([]string, len(m.wts))
	ages := make([]string, len(m.wts))
	width, ageWidth := 0, 0
	for i, wt := range m.wts {
		branches[i] = wt.Branch
		if branches[i] == "" {
			branches[i] = "(detached)"
		}
		if w := lipgloss.Width(branches[i]); w > width {
			width = w
		}
		ages[i] = humanizeAgo(wt.ModTime, now)
		if w := lipgloss.Width(ages[i]); w > ageWidth {
			ageWidth = w
		}
	}

	for i, wt := range m.wts {
		pinned := m.pinned != "" && sameDir(wt.Path, m.pinned)
		cursor := "  "
		pad := strings.Repeat(" ", width-lipgloss.Width(branches[i]))
		label := branches[i]
		switch {
		case i == m.cursor:
			cursor = wtCursorStyle.Render("▸ ")
			label = wtSelectedStyle.Render(label) // cyan, like the other menus' cursor row
		case pinned:
			label = wtPinStyle.Render(label) // green marks the active route at a glance
		default:
			label = wtBranchStyle.Render(label) // rig-blue accent for the rest
		}
		// The age column is right-aligned and dim, then the path, then the pin mark.
		age := ""
		if ageWidth > 0 {
			age = DimStyle.Render(fmt.Sprintf("%*s", ageWidth, ages[i])) + "  "
		}
		mark := ""
		if pinned {
			mark = "  " + wtPinStyle.Render("● pinned")
		}
		b.WriteString(fmt.Sprintf("%s%s%s  %s%s%s\n", cursor, label, pad, age, DimStyle.Render(wt.Path), mark))
	}
	b.WriteString("\n" + DimStyle.Render("↑/↓ move · enter run · p pin · u unpin · q quit") + "\n")
	return b.String()
}

// newWorktreeMenuCmd powers a bare `<tool>-wt`: an interactive worktree menu.
// Choosing "run" prints the worktree path to stdout (the launcher then builds and
// runs it); "pin"/"unpin" change the route here and print nothing to stdout, so
// the launcher has nothing to run. The menu draws on stderr to keep stdout clean
// for the path. Without a TTY it degrades to the same auto-resolution as `pick`
// with no query (lone worktree, or main), printing the path.
func newWorktreeMenuCmd() *cobra.Command {
	var repoDir string
	cmd := &cobra.Command{
		Use:    "menu",
		Short:  "Interactive worktree menu: run one or pin it as the -dev route (used by <tool>-wt)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			routeKey, wts, err := worktreesFor(ctx, repoDir)
			if err != nil {
				return err
			}
			pinned, err := devroute.Read(routeKey)
			if err != nil {
				return err
			}
			if !pickerTTY() {
				// No terminal for the menu → behave like `pick` with no query.
				chosen, err := resolveWorktree(wts, "")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), chosen)
				return nil
			}
			res, err := tea.NewProgram(newWtMenu(wts, pinned), tea.WithOutput(os.Stderr)).Run()
			if err != nil {
				return err
			}
			final, ok := res.(wtMenuModel)
			if !ok {
				return nil // unexpected final model → cancel (launcher sees empty stdout)
			}
			errOut := cmd.ErrOrStderr()
			switch final.action {
			case wtRun:
				fmt.Fprintln(cmd.OutOrStdout(), final.chosen)
			case wtPin:
				if err := devroute.Write(routeKey, final.chosen); err != nil {
					return err
				}
				fmt.Fprintf(errOut, "%s pinned -dev route → %s\n", OkStyle.Render("✓"), HeaderStyle.Render(branchAt(wts, final.chosen)))
				fmt.Fprintf(errOut, "  %s\n", DimStyle.Render(final.chosen))
			case wtUnpin:
				if err := devroute.Unset(routeKey); err != nil {
					return err
				}
				fmt.Fprintf(errOut, "%s cleared the pinned -dev route\n", OkStyle.Render("✓"))
			case wtCancel:
				// Nothing chosen — the launcher sees empty stdout and exits.
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoDir, "repo", "", "repo directory whose worktrees to use (default: current directory)")
	return cmd
}
