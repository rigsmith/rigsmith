package commands

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/mover"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// NewRerootCmd re-files one session under a different directory.
//
// Separate from `mv`, which exists because a directory MOVED and its history has
// to follow. Nothing moves here: a session is filed under wherever Claude Code
// was started, the agent then works wherever the work is, and this says where
// the conversation belongs. It takes the directory you name — there is no
// heuristic and nothing is guessed.
func NewRerootCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "reroot <session-id> <dir>",
		Short: "Re-file one session under a different directory",
		Long: "Claude Code files a session under the directory it was launched in, which\n" +
			"is often not where the work happened — start in a folder that holds your\n" +
			"projects, work in one of them, and the conversation is filed under the\n" +
			"folder instead of the project.\n\n" +
			"This re-files a session you name under a directory you name: the records\n" +
			"recorded at its old root are rewritten to the new one and the transcript\n" +
			"moves into the matching project directory, so `claude --resume` opens it\n" +
			"in the right place.\n\n" +
			"Records from DEEPER directories are left alone — those name real paths\n" +
			"that never moved. Use `mv` when a directory has actually moved on disk.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			id := args[0]

			dst, err := filepath.Abs(args[1])
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
				return errors.New("could not resolve ~/.claude location for this machine")
			}
			projects := filepath.Join(claudeHome, "projects")

			path, oldCwd, err := mover.FindSession(projects, id)
			if err != nil {
				return err
			}

			// Rewriting a transcript Claude Code is appending to would race it.
			// Matched on session id, not on directory: several conversations run
			// out of one folder at once, and refusing because a DIFFERENT chat is
			// open there blocks the move for no reason. This is the same test
			// `delete` uses for the same hazard.
			for _, inst := range account.RunningInstances(claudeHome) {
				if inst.SessionID != "" && session.CanonicalID(inst.SessionID) == session.CanonicalID(id) {
					return fmt.Errorf("that session is running right now (pid %d) — close it first", inst.PID)
				}
			}

			fmt.Fprintf(out, "\n  %s\n  %s → %s\n", DimStyle.Render(filepath.Base(path)), oldCwd, dst)

			mv, err := mover.MoveSession(projects, id, dst, dryRun)
			if err != nil {
				return err
			}
			if mv.OldCwd == mv.NewCwd {
				fmt.Fprintln(out, DimStyle.Render("  already filed there — nothing to do"))
				return nil
			}
			if dryRun {
				fmt.Fprintf(out, "  %s\n",
					DimStyle.Render(fmt.Sprintf("dry run: would rewrite %s and re-file it",
						countOf(mv.Records, "record", "records"))))
				return nil
			}
			fmt.Fprintf(out, "  %s rewrote %s, filed under %s\n", OkStyle.Render("✓"),
				countOf(mv.Records, "record", "records"), filepath.Base(filepath.Dir(mv.NewPath)))
			fmt.Fprintf(out, "  %s\n\n", DimStyle.Render("the next `clauderig sync` carries this to your other machines"))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would change, without changing it")
	return cmd
}
