package commands

import (
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/contents"
	"github.com/spf13/cobra"
)

// repackFloorBytes is the size below which repacking has nothing to give back,
// whatever the ratio says. Git's own overhead — refs, the index, a couple of
// packs — is a fixed cost of tens of kilobytes, so a small repo always looks
// lopsided and always will.
const repackFloorBytes = 64 << 20

// NewRepoCmd builds `codexrig repo`: how big the backup is, what is in it, and
// the two things that can be done about it.
func NewRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "How large the sync repo is, and what is taking the room",
		Long: "A byte total on its own invites the wrong lever: the instinct is to squash\n" +
			"history, and on a repo that is mostly conversation that moves nothing. This\n" +
			"reports the split, so the answer can be retention, or nothing at all.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newRepoStatusCmd(), newRepoGCCmd())
	return cmd
}

func newRepoStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"size"},
		Short:   "What the repo holds, by category and size",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			rep, err := contents.Scan(staging)
			if err != nil {
				return err
			}

			var gitBytes, workBytes int64
			var stats gitrepo.Stats
			haveGit := false
			if repo, oerr := gitrepo.Open(cmd.Context(), staging); oerr == nil {
				gitBytes, _ = repo.GitDirBytes(cmd.Context())
				workBytes, _ = repo.WorkTreeBytes(cmd.Context())
				if s, serr := repo.Stats(cmd.Context()); serr == nil {
					stats, haveGit = s, true
				}
			}

			if asJSON {
				return writeJSON(out, map[string]any{
					"files": rep.Files, "bytes": rep.Bytes,
					"groups": rep.Groups, "gitBytes": gitBytes, "workTreeBytes": workBytes,
					"commits": stats.Commits,
				})
			}

			fmt.Fprintln(out, HeaderStyle.Render("Sync repo"))
			fmt.Fprintf(out, "  %-12s %s\n", "location", staging)
			fmt.Fprintf(out, "  %-12s %s across %d file(s)\n", "contents", humanBytes(rep.Bytes), rep.Files)
			if gitBytes > 0 {
				fmt.Fprintf(out, "  %-12s %s\n", "git history", humanBytes(gitBytes))
			}
			if haveGit && stats.Commits > 0 {
				fmt.Fprintf(out, "  %-12s %d\n", "commits", stats.Commits)
				if !stats.First.IsZero() {
					fmt.Fprintf(out, "  %-12s %s\n", "since", stats.First.Local().Format("2006-01-02"))
				}
			}

			if rep.Files == 0 {
				fmt.Fprintf(out, "\n  %s\n", DimStyle.Render("nothing synced yet"))
				return nil
			}
			fmt.Fprintln(out)
			for _, g := range rep.Fold().Groups {
				share := float64(g.Bytes) / float64(rep.Bytes) * 100
				fmt.Fprintf(out, "  %-18s %9s  %4.0f%%  %s\n",
					g.Name, humanBytes(g.Bytes), share, DimStyle.Render(g.Detail))
			}

			// Only say something when there is something to do about it, and a
			// RATIO alone is not that. Every fresh repo has more git metadata
			// than content — this one had 28 KB of git against 483 bytes of
			// files, which is a perfect score on the ratio and nothing anybody
			// should act on. The floor is what makes the advice mean
			// "reclaimable", rather than "small".
			if gitBytes > repackFloorBytes && workBytes > 0 && gitBytes > 2*workBytes {
				fmt.Fprintf(out, "\n  %s\n", WarnStyle.Render("git history is more than twice the working tree"))
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("`codexrig repo gc` repacks it, which usually reclaims most of that and loses nothing"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func newRepoGCCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gc",
		Short: "Repack the repo's history (loses nothing)",
		Long: "Runs git's own repack over the sync repo. It is the first thing to try and\n" +
			"almost always the last: on a real repo, 2.4 GB of a 2.9 GB .git was simply\n" +
			"unpacked, and repacking gave it back without touching a single commit.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			repo, err := gitrepo.Open(cmd.Context(), staging)
			if err != nil {
				return fmt.Errorf("no sync repo on this machine yet")
			}
			before, _ := repo.GitDirBytes(cmd.Context())
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("repacking "+humanBytes(before)+" — this can take a while"))

			start := time.Now()
			if err := repo.Repack(cmd.Context()); err != nil {
				return err
			}
			after, _ := repo.GitDirBytes(cmd.Context())
			switch {
			case before > 0 && after < before:
				fmt.Fprintf(out, "%s %s → %s (%s reclaimed in %s)\n", OkStyle.Render("repacked"),
					humanBytes(before), humanBytes(after), humanBytes(before-after), time.Since(start).Round(time.Second))
			default:
				fmt.Fprintf(out, "%s %s\n", OkStyle.Render("repacked"), DimStyle.Render("already compact — nothing to reclaim"))
			}
			return nil
		},
	}
}
