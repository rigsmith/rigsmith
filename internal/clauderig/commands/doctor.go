package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/doctor"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds the `doctor` command — a health check across the
// environment, the sync setup, and this repo's worktree-discipline wiring. It
// prints a sectioned report, then (on a TTY) offers a multi-select of the issues
// clauderig can repair, all pre-checked so Enter fixes everything. `--fix` applies
// them non-interactively. doctor exits non-zero while any failing check remains, so
// it's usable in scripts.
func NewDoctorCmd(version string) *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check clauderig: environment, sync, worktree discipline (--fix to repair)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, version, fixAll)
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "apply every fixable issue without prompting")
	return cmd
}

func runDoctor(cmd *cobra.Command, version string, fixAll bool) error {
	// Bound the network-touching checks (remote reachability/privacy) so doctor
	// can't hang.
	ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Second)
	defer cancel()
	out := cmd.OutOrStdout()

	env := buildDoctorEnv(ctx, version)
	sections := doctor.Run(ctx, env)
	renderHeader(out, env)
	renderSections(out, sections)
	fails, warns, _ := doctor.Counts(sections)
	fixable := doctor.Fixable(sections)
	renderSummary(out, fails, warns, len(fixable))

	chosen := chooseFixes(out, fixable, fixAll)
	if len(chosen) > 0 {
		fails -= applyFixes(ctx, out, chosen)
	}

	if fails > 0 {
		os.Exit(1)
	}
	return nil
}

func buildDoctorEnv(ctx context.Context, version string) doctor.Env {
	home, _ := os.UserHomeDir()
	root := repoRootBestEffort(ctx)
	env := doctor.Env{Home: home, Version: version, RepoRoot: root}
	if root != "" {
		env.RepoName = filepath.Base(root)
		env.ProjectSettings, _ = settings.Project.Path(home, root)
		env.LocalSettings, _ = settings.Local.Path(home, root)
		env.ClaudeMd = filepath.Join(root, "CLAUDE.md")
	}
	env.UserSettings, _ = settings.User.Path(home, root)
	cfg, err := config.LoadOrDefault()
	if err != nil {
		cfg = config.Default()
	}
	env.Cfg = cfg
	env.Machine = config.Detect("this")
	env.Staging, _ = config.StagingDir()
	return env
}

// chooseFixes decides which fixes to apply: all with --fix, an interactive
// pre-checked multi-select on a TTY, or none when non-interactive.
func chooseFixes(out io.Writer, fixable []doctor.Result, fixAll bool) []doctor.Result {
	if len(fixable) == 0 {
		return nil
	}
	if fixAll {
		return fixable
	}
	if !interactive() {
		fmt.Fprintln(out, DimStyle.Render("  run `clauderig doctor --fix` to apply the fixable issues"))
		return nil
	}
	return selectFixes(out, fixable)
}

func selectFixes(out io.Writer, fixable []doctor.Result) []doctor.Result {
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
	)).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if err != nil {
		fmt.Fprintln(out, DimStyle.Render("  (no fixes applied)"))
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
			fmt.Fprintf(out, "  %s %s: %v\n", ErrStyle.Render("✗"), r.FixLabel, err)
			continue
		}
		fmt.Fprintf(out, "  %s %s\n", OkStyle.Render("✓"), r.FixLabel)
		if r.Status == doctor.Fail {
			fixedFails++
		}
	}
	return fixedFails
}

func renderHeader(out io.Writer, env doctor.Env) {
	fmt.Fprintf(out, "%s   %s\n\n", HeaderStyle.Render("clauderig doctor"),
		DimStyle.Render(env.Machine.OS+" · clauderig "+env.Version))
}

func renderSections(out io.Writer, sections []doctor.Section) {
	for _, s := range sections {
		fmt.Fprintln(out, DimStyle.Render(s.Title))
		for _, r := range s.Results {
			fmt.Fprintf(out, "  %s %-22s %s\n", glyph(r.Status), r.Name, r.Detail)
			if r.Hint != "" && r.Status != doctor.OK {
				fmt.Fprintf(out, "    %s\n", DimStyle.Render("→ "+r.Hint))
			}
		}
		fmt.Fprintln(out)
	}
}

func renderSummary(out io.Writer, fails, warns, fixable int) {
	if fails == 0 && warns == 0 {
		fmt.Fprintln(out, OkStyle.Render("✓ all good"))
		return
	}
	var parts []string
	if fails > 0 {
		parts = append(parts, ErrStyle.Render(fmt.Sprintf("%d failing", fails)))
	}
	if warns > 0 {
		parts = append(parts, WarnStyle.Render(fmt.Sprintf("%d warning(s)", warns)))
	}
	fmt.Fprintf(out, "%s — %s fixable\n", strings.Join(parts, ", "), OkStyle.Render(fmt.Sprintf("%d", fixable)))
}

func glyph(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return OkStyle.Render("✓")
	case doctor.Warn:
		return WarnStyle.Render("!")
	case doctor.Fail:
		return ErrStyle.Render("✗")
	default:
		return DimStyle.Render("·")
	}
}
