// Package doctorui is the shared terminal presentation for every rig's `doctor`
// command. It renders a core/doctor report and runs the fix-on-request flow — a
// pre-checked multi-select on a TTY, `--fix` to apply every fixable issue
// non-interactively — so "install the missing tool for me" behaves identically
// across rig, clauderig, changerig and shiprig. The checks and the install/repair
// closures live in each tool; this package only presents and applies them.
package doctorui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/spf13/cobra"
)

// Shared semantic styles for the report. The status colors are common to every
// tool (only the interactive picker's accent differs, see Options.Accent).
var (
	dimStyle  = lipgloss.NewStyle().Foreground(brand.Muted)
	okStyle   = lipgloss.NewStyle().Foreground(brand.Green)
	warnStyle = lipgloss.NewStyle().Foreground(brand.Yellow)
	errStyle  = lipgloss.NewStyle().Foreground(brand.Red)
)

// Options carries the tool-specific policy doctorui can't decide itself: the
// brand accent for the picker, whether `--fix` was passed (apply all, no prompt),
// and whether prompting is allowed (a TTY and not suppressed — each tool computes
// this with its own quiet/TTY rules).
type Options struct {
	Accent      lipgloss.AdaptiveColor
	FixAll      bool
	Interactive bool
}

// glyph returns the colored status mark for a check.
func glyph(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return okStyle.Render("✓")
	case doctor.Warn:
		return warnStyle.Render("!")
	case doctor.Fail:
		return errStyle.Render("✗")
	default:
		return dimStyle.Render("·")
	}
}

// RenderSections prints the sectioned ✓/!/✗ report, each degraded/failing check
// followed by its hint. Tools with their own checklist renderer (rig's live
// spinner) skip this and render the checks themselves; the shared fix flow
// (RunFixes) still applies.
func RenderSections(out io.Writer, sections []doctor.Section) {
	for _, s := range sections {
		fmt.Fprintln(out, dimStyle.Render(s.Title))
		for _, r := range s.Results {
			fmt.Fprintf(out, "  %s %-22s %s\n", glyph(r.Status), r.Name, r.Detail)
			if r.Hint != "" && r.Status != doctor.OK {
				fmt.Fprintf(out, "    %s\n", dimStyle.Render("→ "+r.Hint))
			}
		}
		fmt.Fprintln(out)
	}
}

// RenderSummary prints the one-line verdict: N failing, M warning(s), K fixable.
func RenderSummary(out io.Writer, sections []doctor.Section) {
	fails, warns, fixable := doctor.Counts(sections)
	if fails == 0 && warns == 0 {
		fmt.Fprintln(out, okStyle.Render("✓ all good"))
		return
	}
	var parts []string
	if fails > 0 {
		parts = append(parts, errStyle.Render(fmt.Sprintf("%d failing", fails)))
	}
	if warns > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d warning(s)", warns)))
	}
	fmt.Fprintf(out, "%s — %s fixable\n", strings.Join(parts, ", "), okStyle.Render(fmt.Sprintf("%d", fixable)))
}

// RunFixes offers the fixable issues and applies the chosen ones, returning how
// many *failing* checks remain unresolved so the caller can set its exit code.
// Selection: FixAll applies all without prompting; otherwise an interactive,
// pre-checked multi-select on a TTY; non-interactively it prints how to apply and
// changes nothing.
func RunFixes(cmd *cobra.Command, sections []doctor.Section, opts Options) (failsRemaining int) {
	out := cmd.OutOrStdout()
	fails, _, _ := doctor.Counts(sections)
	chosen := chooseFixes(cmd, doctor.Fixable(sections), opts)
	if len(chosen) > 0 {
		fails -= applyFixes(cmd.Context(), out, chosen)
	}
	return fails
}

// chooseFixes decides which fixes to apply: all with FixAll, an interactive
// pre-checked multi-select on a TTY, or none (with a how-to hint) otherwise.
func chooseFixes(cmd *cobra.Command, fixable []doctor.Result, opts Options) []doctor.Result {
	out := cmd.OutOrStdout()
	if len(fixable) == 0 {
		return nil
	}
	if opts.FixAll {
		return fixable
	}
	if !opts.Interactive {
		fmt.Fprintln(out, dimStyle.Render(fmt.Sprintf("  run `%s --fix` to apply the fixable issues", cmd.CommandPath())))
		return nil
	}
	return selectFixes(out, fixable, opts.Accent)
}

func selectFixes(out io.Writer, fixable []doctor.Result, accent lipgloss.AdaptiveColor) []doctor.Result {
	var picked []int
	opts := make([]huh.Option[int], len(fixable))
	for i, r := range fixable {
		opts[i] = huh.NewOption(r.FixLabel, i).Selected(true)
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[int]().
			Title("Fix which issues? (all selected — space toggles, enter applies)").
			Options(opts...).
			Value(&picked),
	)).WithTheme(brand.Theme(accent)).Run()
	if err != nil {
		fmt.Fprintln(out, dimStyle.Render("  (no fixes applied)"))
		return nil
	}
	chosen := make([]doctor.Result, 0, len(picked))
	for _, i := range picked {
		chosen = append(chosen, fixable[i])
	}
	return chosen
}

// applyFixes runs each chosen fix and returns how many *failing* checks it cleared,
// so the caller can drop them from the exit-code tally.
func applyFixes(ctx context.Context, out io.Writer, chosen []doctor.Result) (fixedFails int) {
	fmt.Fprintln(out)
	for _, r := range chosen {
		if err := r.Fix(ctx); err != nil {
			fmt.Fprintf(out, "  %s %s: %v\n", errStyle.Render("✗"), r.FixLabel, err)
			continue
		}
		fmt.Fprintf(out, "  %s %s\n", okStyle.Render("✓"), r.FixLabel)
		if r.Status == doctor.Fail {
			fixedFails++
		}
	}
	return fixedFails
}
