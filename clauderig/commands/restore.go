package commands

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/engine"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/core/pathmap"
	"github.com/spf13/cobra"
)

// NewRestoreCmd builds the `restore` command — ensure/pull the staging repo, then
// write the allowlist back to this machine with project slugs rewritten for this
// OS (via the manifest) and redacted config merged so local secrets survive. On a
// non-empty ~/.claude it refuses unless --backup or --force (safe default for
// non-interactive/hook contexts).
func NewRestoreCmd() *cobra.Command {
	var backup, force bool
	var dir string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore your Claude Code setup here, rewriting paths for this OS",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			staging, err := config.StagingDir()
			if err != nil {
				return err
			}

			fmt.Fprintln(out, HeaderStyle.Render("clauderig restore"))

			// Ensure the staging repo: open + pull, or clone from the remote.
			if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
				repo, err := gitrepo.Open(ctx, staging)
				if err != nil {
					return err
				}
				if cfg.Remote != "" {
					if err := repo.Pull(ctx, "origin", "main"); err != nil {
						fmt.Fprintf(out, "  %s pull skipped: %v\n", WarnStyle.Render("⚠"), err)
					}
				}
			} else if cfg.Remote != "" {
				if _, err := gitrepo.Clone(ctx, cfg.Remote, staging); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("no local staging repo and no remote configured — run `clauderig sync` or `clauderig init` first")
			}

			man, err := manifest.Load(staging)
			if err != nil {
				return fmt.Errorf("read manifest (nothing synced yet?): %w", err)
			}

			// --dir: restore only the CLI payload into a test folder, never the
			// real ~/.claude or the desktop root.
			var opts engine.RestoreOptions
			if dir != "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				opts.TargetOverride = map[string]string{"cli": abs}
				opts.OverriddenOnly = true
				fmt.Fprintf(out, "  %s restoring CLI root into %s\n", DimStyle.Render("→"), abs)
			}

			// Safety: refuse to write over a non-empty CLI target unless told to.
			cliTarget, st := cfg.RootLocation("cli", me)
			if dir != "" {
				cliTarget, st = opts.TargetOverride["cli"], pathmap.StatusResolved
			}
			if st == pathmap.StatusResolved && nonEmptyDir(cliTarget) && !force {
				if !backup {
					return fmt.Errorf("%s is not empty; re-run with --backup (copy it aside first) or --force", cliTarget)
				}
				bak := cliTarget + ".bak"
				if _, err := os.Stat(bak); err == nil {
					return fmt.Errorf("backup %s already exists; move it away first", bak)
				}
				fmt.Fprintf(out, "  backing up %s → %s\n", cliTarget, bak)
				if err := copyTree(cliTarget, bak); err != nil {
					return fmt.Errorf("backup: %w", err)
				}
			}

			opts.StagingDir = staging
			opts.Config = cfg
			opts.Machine = me
			opts.Manifest = man
			rep, err := engine.Restore(opts)
			if err != nil {
				return err
			}
			for _, r := range rep.Roots {
				if r.Skipped {
					fmt.Fprintf(out, "  %-8s %s\n", r.ID, DimStyle.Render("skipped (nothing staged)"))
					continue
				}
				fmt.Fprintf(out, "  %-8s %d files, %d slug(s) rewritten for this machine\n", r.ID, r.Files, r.SlugsRewritten)
			}
			if man.ClaudeVersion != "" {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("synced from Claude Code "+man.ClaudeVersion))
			}
			fmt.Fprintln(out, OkStyle.Render("\n  ✓ restored"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&backup, "backup", false, "copy an existing ~/.claude to ~/.claude.bak before restoring")
	cmd.Flags().BoolVar(&force, "force", false, "restore over an existing ~/.claude without prompting")
	cmd.Flags().StringVar(&dir, "dir", "", "restore the CLI payload into this folder instead of ~/.claude (test/inspect)")
	return cmd
}

func nonEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// copyTree recursively copies src to dst (used for the pre-restore backup).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyOne(p, target)
	})
}

func copyOne(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
