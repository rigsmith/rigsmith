package commands

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/engine"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/clauderig/internal/project"
	"github.com/rigsmith/core/brand"
	"github.com/rigsmith/core/pathmap"
	"github.com/spf13/cobra"
)

// NewRestoreCmd builds the `restore` command — ensure/pull the staging repo, then
// write the allowlist back to this machine with project slugs rewritten for this
// OS (via the manifest) and redacted config merged so local secrets survive. On a
// non-empty ~/.claude it refuses unless --backup or --force (safe default for
// non-interactive/hook contexts).
func NewRestoreCmd() *cobra.Command {
	var backup, force, prune bool
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

			cliTarget, st := cfg.RootLocation("cli", me)
			if dir != "" {
				cliTarget, st = opts.TargetOverride["cli"], pathmap.StatusResolved
			}

			// Preview what restore will do before touching anything.
			printRestorePreview(out, man, me, cliTarget)

			// Safety: don't write over a non-empty target unless told to. Prompt
			// interactively; non-interactively default to abort.
			if st == pathmap.StatusResolved && nonEmptyDir(cliTarget) && !force && !backup {
				switch chooseRestoreSafety(cliTarget) {
				case "backup":
					backup = true
				case "inplace":
					force = true
				default:
					return fmt.Errorf("aborted: %s is not empty (re-run with --backup or --force)", cliTarget)
				}
			}
			if backup {
				bak := cliTarget + ".bak"
				if _, err := os.Stat(bak); err == nil {
					return fmt.Errorf("backup %s already exists; move it away first", bak)
				}
				fmt.Fprintf(out, "  backing up %s → %s\n", cliTarget, bak)
				if err := copyTree(cliTarget, bak); err != nil {
					return fmt.Errorf("backup: %w", err)
				}
			}

			// Prune defaults to the config's AlwaysPrune; an explicit --prune
			// (true or false) overrides it for this run.
			opts.Prune = cfg.AlwaysPrune
			if cmd.Flags().Changed("prune") {
				opts.Prune = prune
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
				pruned := ""
				if r.Pruned > 0 {
					pruned = fmt.Sprintf(", %d pruned", r.Pruned)
				}
				fmt.Fprintf(out, "  %-8s %d files, %d slug(s) rewritten%s\n", r.ID, r.Files, r.SlugsRewritten, pruned)
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
	cmd.Flags().BoolVar(&prune, "prune", false, "remove config files (skills/commands/agents/plans) deleted upstream; never touches projects")
	return cmd
}

func nonEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// printRestorePreview shows where restore lands and a few sample slug rewrites for
// this machine, before anything is written.
func printRestorePreview(out io.Writer, man *manifest.Manifest, me config.Machine, target string) {
	fmt.Fprintln(out, DimStyle.Render("  preview:"))
	fmt.Fprintf(out, "  target    %s\n", target)
	if man.ClaudeVersion != "" {
		fmt.Fprintf(out, "  source    %s\n", DimStyle.Render("Claude Code "+man.ClaudeVersion))
	}
	res := me.Resolver()
	shown := 0
	for _, slug := range man.Slugs() {
		p := man.Projects[slug]
		if p.Template == "" {
			continue
		}
		ns, _, stt := project.RewriteFromTemplate(p.Template, res)
		if stt != pathmap.StatusResolved || ns == slug {
			continue
		}
		fmt.Fprintf(out, "  rewrite   %s → %s\n", slug, ns)
		if shown++; shown >= 3 {
			break
		}
	}
	fmt.Fprintf(out, "  projects  %d\n", len(man.Projects))
}

// chooseRestoreSafety prompts (interactively) for how to handle a non-empty
// target. Non-interactively it returns "abort" (the safe default for hooks/CI).
func chooseRestoreSafety(target string) string {
	if !interactive() {
		return "abort"
	}
	choice := "backup"
	_ = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(target+" is not empty — restore how?").
			Options(
				huh.NewOption("Back up to .bak, then restore", "backup"),
				huh.NewOption("Restore in place (config merges; local secrets kept)", "inplace"),
				huh.NewOption("Abort", "abort"),
			).Value(&choice),
	)).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	return choice
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
