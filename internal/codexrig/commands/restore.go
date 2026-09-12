package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/engine"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
	"github.com/rigsmith/rigsmith/internal/codexrig/manifest"
	"github.com/rigsmith/rigsmith/internal/codexrig/service"
	"github.com/spf13/cobra"
)

// NewRestoreCmd builds `codexrig restore`.
func NewRestoreCmd() *cobra.Command {
	var force, prune bool
	var dir string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Write the synced setup onto this machine",
		Long: "Unpacks the snapshot into your Codex home, resolving portable paths onto this\n" +
			"machine and keeping this machine's own secrets where a field was redacted.\n\n" +
			"It never writes a credential — auth.json is not in the backup — so a restored\n" +
			"machine still runs `codex login` once. A rollout already here is left alone.\n\n" +
			"--dir unpacks somewhere harmless instead, which is the way to look before you\n" +
			"leap.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}
			me := config.DetectFor(cfg)

			if err := ensureStaging(cmd, cfg, staging); err != nil {
				return err
			}
			man, err := manifest.Load(staging)
			if os.IsNotExist(err) {
				return errors.New("no snapshot in the sync repo yet — run `codexrig sync` on a machine that has one")
			} else if err != nil {
				// A corrupt or unreadable manifest is a different problem, and
				// the advice above sends the reader to fix the wrong machine.
				return fmt.Errorf("reading the snapshot manifest: %w", err)
			}

			opts := engine.RestoreOptions{
				StagingDir: staging, Config: cfg, Machine: me, Manifest: man,
				Prune: prune || cfg.AlwaysPrune,
			}
			target, _ := cfg.RootLocation(config.RootCLI, me)
			if dir != "" {
				abs, aerr := filepath.Abs(dir)
				if aerr != nil {
					return aerr
				}
				opts.TargetOverride = map[string]string{config.RootCLI: abs}
				target = abs
			}

			fmt.Fprintf(out, "%s\n", HeaderStyle.Render("restore"))
			fmt.Fprintf(out, "  into       %s\n", target)
			fmt.Fprintf(out, "  snapshot   %s\n", snapshotLine(man))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("auth.json is not in the backup; this machine logs in for itself"))

			// Writing over an established home is the one irreversible thing
			// here, so it is confirmed rather than assumed — except into a
			// scratch directory, where there is nothing to lose.
			if dir == "" && !force && establishedHome(target) {
				if !interactive() {
					return errors.New("this machine already has a Codex setup; re-run with --force, or use --dir to unpack somewhere harmless first")
				}
				ok, cerr := confirm("Write the snapshot over this machine's existing Codex setup?")
				if cerr != nil {
					return cerr
				}
				if !ok {
					fmt.Fprintln(out, DimStyle.Render("aborted — nothing written"))
					return nil
				}
			}

			rep, rerr := engine.Restore(opts)
			if rerr != nil {
				return rerr
			}
			for _, rr := range rep.Roots {
				if rr.Absent {
					fmt.Fprintf(out, "  %-6s %s\n", rr.ID, DimStyle.Render("nothing staged for this root"))
					continue
				}
				fmt.Fprintf(out, "  %-6s %d written · %d merged · %d rollouts kept\n", rr.ID, rr.Written, rr.Merged, rr.Kept)
				if rr.Conflict > 0 {
					fmt.Fprintf(out, "    %s %d path(s) blocked by a directory or a file in the way\n", WarnStyle.Render("!"), rr.Conflict)
				}
				if rr.Pruned > 0 {
					fmt.Fprintf(out, "    %d removed (deleted upstream)\n", rr.Pruned)
				}
			}
			if len(rep.CommentsLost) > 0 {
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render("these files changed and were rewritten, so their comments are gone:"))
				for _, p := range rep.CommentsLost {
					fmt.Fprintf(out, "    %s\n", p)
				}
			}
			if dir == "" {
				_ = journal.Append(staging, journal.Succeeded(me.Name, journal.OpRestore))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "write over an existing Codex setup without asking")
	cmd.Flags().BoolVar(&prune, "prune", false, "also remove skills, prompts and rules deleted on another machine")
	cmd.Flags().StringVar(&dir, "dir", "", "unpack into this directory instead of the real Codex home")
	return cmd
}

// ensureStaging makes sure there is a snapshot to restore from, cloning when the
// machine has a remote but no local copy.
func ensureStaging(cmd *cobra.Command, cfg *config.Config, staging string) error {
	if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
		repo, oerr := gitrepo.Open(cmd.Context(), staging)
		if oerr == nil && cfg.Remote != "" {
			// Best-effort: a restore from the copy already here beats no
			// restore because the network is down.
			_ = repo.Pull(cmd.Context(), service.Remote, service.Branch)
		}
		return nil
	}
	if cfg.Remote == "" {
		return errors.New("no sync repo on this machine and no remote configured — run `codexrig init`")
	}
	_, err := gitrepo.Clone(cmd.Context(), cfg.Remote, staging)
	return err
}

// establishedHome reports whether this machine already has a Codex setup worth
// asking about before overwriting.
func establishedHome(home string) bool {
	if _, err := os.Stat(codexhome.Config(home)); err == nil {
		return true
	}
	entries, err := os.ReadDir(codexhome.Skills(home))
	return err == nil && len(entries) > 0
}

func snapshotLine(m *manifest.Manifest) string {
	line := m.SourceOS
	if m.CodexVersion != "" {
		line += " · codex " + m.CodexVersion
	}
	if n := len(m.Cwds); n > 0 {
		line += fmt.Sprintf(" · %d project director%s", n, plural(n))
	}
	return line
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
