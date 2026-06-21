package commands

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/mover"
	"github.com/spf13/cobra"
)

// NewMoveCmd builds the `mv` command — move or rename a directory and relink its
// Claude Code history so the conversation stays attached. Claude keys a project's
// sessions by a slug derived from the directory's absolute path; moving the
// directory without renaming the slug orphans the history. This renames the slug
// dir(s), rebases the cwd recorded in the transcripts, and follows the same path
// through the Desktop session metadata and settings additionalDirectories.
func NewMoveCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "mv <src> <dst>",
		Short: "Move/rename a directory and relink its Claude history",
		Long: "Move or rename a directory and bring its Claude Code conversation history\n" +
			"with it. Claude stores each project's sessions under a slug derived from the\n" +
			"directory's path, so a plain mv orphans the history — this renames the slug\n" +
			"dir(s), rebases the cwd inside the transcripts, and updates the Desktop\n" +
			"session metadata and settings additionalDirectories to match.\n\n" +
			"Run it from outside the directory being moved. If you already moved the\n" +
			"directory by hand, pass the old and new paths to relink the history alone.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			absSrc, absDst, moveDir, err := mover.Resolve(args[0], args[1])
			if err != nil {
				return err
			}

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			claudeHome, st := cfg.RootLocation("cli", me)
			if st != pathmap.StatusResolved {
				return fmt.Errorf("could not resolve ~/.claude location for this machine")
			}
			desktopRoot, dst := cfg.RootLocation("desktop", me)
			if dst != pathmap.StatusResolved {
				desktopRoot = "" // Desktop not present/configured — skip it
			}

			liveCwds := sessionCwds(account.RunningInstances(claudeHome))
			plan, err := mover.BuildPlan(absSrc, absDst, moveDir, claudeHome, desktopRoot, liveCwds)
			if err != nil {
				return err
			}

			printMovePlan(out, plan)

			if len(plan.LiveBlockers) > 0 {
				return fmt.Errorf("aborted: close the running Claude session(s) inside %s, then re-run", absSrc)
			}
			if plan.HasCollision() {
				return fmt.Errorf("aborted: the destination already has Claude history (a session was opened there); merge or remove it first")
			}
			if !plan.MoveDir && plan.Empty() {
				fmt.Fprintln(out, DimStyle.Render("  nothing to relink — no history references this path"))
				return nil
			}

			if dryRun {
				fmt.Fprintln(out, DimStyle.Render("  (dry run — nothing changed)"))
				return nil
			}
			if !Interactive() {
				return fmt.Errorf("refusing to move history non-interactively; re-run in a terminal, or pass --dry-run to preview")
			}
			ok, err := confirmDestructive(fmt.Sprintf("Move %s → %s and relink its Claude history?",
				filepath.Base(absSrc), filepath.Base(absDst)))
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted")
			}

			rep, err := plan.Apply(filepath.Join(claudeHome, "projects"), false)
			if err != nil {
				return err
			}
			printMoveReport(out, plan, rep)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview the move without changing anything")
	return cmd
}

// sessionCwds extracts the cwds of running CLI/IDE sessions (ide-lock instances
// carry no cwd and are dropped) for the live-session guard.
func sessionCwds(insts []account.Instance) []string {
	var out []string
	for _, i := range insts {
		if i.Cwd != "" {
			out = append(out, i.Cwd)
		}
	}
	return out
}

func printMovePlan(w io.Writer, p *mover.Plan) {
	fmt.Fprintln(w, HeaderStyle.Render("clauderig mv"))
	verb := "relink"
	if p.MoveDir {
		verb = "move + relink"
	}
	fmt.Fprintf(w, "  %s\n", DimStyle.Render(fmt.Sprintf("%s  %s → %s", verb, p.Src, p.Dst)))

	if len(p.LiveBlockers) > 0 {
		fmt.Fprintf(w, "  %s\n", ErrStyle.Render("live sessions inside the source:"))
		for _, c := range p.LiveBlockers {
			fmt.Fprintf(w, "    %s\n", ErrStyle.Render(c))
		}
	}
	for _, m := range p.Projects {
		mark := ""
		if m.Collision {
			mark = "  " + ErrStyle.Render("(destination slug already exists)")
		}
		fmt.Fprintf(w, "  %s %s → %s%s\n", DimStyle.Render("project"), m.OldSlug, m.NewSlug, mark)
	}
	if len(p.Desktop) > 0 {
		fmt.Fprintf(w, "  %s %d Desktop session file(s)\n", DimStyle.Render("desktop"), len(p.Desktop))
	}
	if p.Settings != "" {
		fmt.Fprintf(w, "  %s settings.json additionalDirectories\n", DimStyle.Render("settings"))
	}
}

func printMoveReport(w io.Writer, p *mover.Plan, r mover.Report) {
	if r.MovedDir {
		fmt.Fprintf(w, "  %s moved %s → %s\n", OkStyle.Render("✓"), p.Src, p.Dst)
	}
	fmt.Fprintf(w, "  %s %d slug(s) renamed, %d transcript record(s) rebased\n",
		OkStyle.Render("✓"), r.SlugsRenamed, r.Transcripts)
	if r.DesktopFiles > 0 {
		fmt.Fprintf(w, "  %s %d Desktop session file(s) updated\n", OkStyle.Render("✓"), r.DesktopFiles)
	}
	if r.SettingsFile {
		fmt.Fprintf(w, "  %s settings.json updated\n", OkStyle.Render("✓"))
	}
}
