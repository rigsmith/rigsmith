package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/doctor"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
	"github.com/rigsmith/rigsmith/internal/doctorui"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds the `doctor` command — a health check across the
// environment, the sync setup, and this repo's worktree-discipline wiring. It
// prints a sectioned report, then (on a TTY) offers a multi-select of the issues
// clauderig can repair, all pre-checked so Enter fixes everything. `--fix` applies
// them non-interactively. doctor exits non-zero while any failing check remains, so
// it's usable in scripts. The report model and the fix flow are shared with the
// other rigs via core/doctor + internal/doctorui.
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
	// can't hang. Fixes run on the command context (uncapped) since an install
	// may take longer than this probe budget.
	ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Second)
	defer cancel()
	out := cmd.OutOrStdout()

	env := buildDoctorEnv(ctx, version)
	sections := doctor.Run(ctx, env)
	renderHeader(out, env)
	doctorui.RenderSections(out, sections)
	doctorui.RenderSummary(out, sections)

	fails := doctorui.RunFixes(cmd, sections, doctorui.Options{
		Accent:      brand.AccentClaude,
		FixAll:      fixAll,
		Interactive: interactive(),
	})
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

func renderHeader(out io.Writer, env doctor.Env) {
	fmt.Fprintf(out, "%s   %s\n\n", HeaderStyle.Render("clauderig doctor"),
		DimStyle.Render(env.Machine.OS+" · clauderig "+env.Version))
}
