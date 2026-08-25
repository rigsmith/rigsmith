package commands

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
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
				if err := backupIdentityFile(out); err != nil {
					return err
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
			// Driven by what is in the repo, not by what this machine already
			// has: restoring onto a new computer is the case that matters, and
			// there the profiles do not exist locally yet.
			opts.Profiles = engine.StagedProfileNames(staging)
			rep, err := engine.Restore(opts)
			if err != nil {
				return err
			}
			w := 0
			for _, r := range rep.Roots {
				w = rootColumn(w, r.ID)
			}
			for _, r := range rep.Roots {
				if r.Skipped {
					fmt.Fprintf(out, "  %-*s %s\n", w, r.ID, DimStyle.Render("skipped (nothing staged)"))
					continue
				}
				extra := ""
				if r.Links > 0 {
					extra += fmt.Sprintf(", %d memory link(s)", r.Links)
				}
				if r.Pruned > 0 {
					extra += fmt.Sprintf(", %d pruned", r.Pruned)
				}
				fmt.Fprintf(out, "  %-*s %d files, %d slug(s) rewritten%s\n", w, r.ID, r.Files, r.SlugsRewritten, extra)
			}
			if man.ClaudeVersion != "" {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("synced from Claude Code "+man.ClaudeVersion))
			}
			if n := rep.DesktopSessions(); n > 0 {
				printDesktopRestartNudge(out, n)
			}
			fmt.Fprintln(out, OkStyle.Render("\n  ✓ restored"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&backup, "backup", false, "copy an existing ~/.claude to ~/.claude.bak before restoring")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "restore over an existing ~/.claude without prompting")
	cmd.Flags().StringVar(&dir, "dir", "", "restore the CLI payload into this folder instead of ~/.claude (test/inspect)")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove config files (skills/commands/agents/plans) deleted upstream; never touches projects")
	return cmd
}

func nonEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// printDesktopRestartNudge tells the user to restart Claude Desktop so it
// re-scans the just-restored Code-session sidecars into its Code-tab list.
// Desktop only rebuilds that list on startup, so without the restart the
// restored sessions stay on disk but invisible.
func printDesktopRestartNudge(out io.Writer, n int) {
	// ⌘Q is macOS-only; on Windows/Linux there's no such shortcut, so drop it.
	quit := "fully quit and reopen Claude Desktop"
	if runtime.GOOS == "darwin" {
		quit = "fully quit Claude Desktop (⌘Q) and reopen"
	}
	fmt.Fprintf(out, "  %s\n", WarnStyle.Render(fmt.Sprintf(
		"%d Desktop Code session(s) restored — %s to see them in the Code tab.", n, quit)))
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
//
// Symlinks are recreated, never followed. ~/.claude is full of shared-memory
// links (worktree slugs pointing memory/ at their main project), and copying
// through one either duplicates the whole linked tree into the backup or fails
// outright when it points at a directory. Link text is reproduced verbatim, so
// an absolute link still resolves to the same place from inside the .bak — the
// same thing `cp -R` does.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return copyLink(p, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyOne(p, target)
	})
}

// copyLink recreates the symlink at src as an identical symlink at dst.
func copyLink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, dst)
}

// backupIdentityFile copies ~/.claude.json alongside the tree backup.
//
// It holds the oauthAccount block — the ONLY record of which account this
// machine is logged in as — and it sits outside ~/.claude, so the tree copy
// above misses it entirely. That is the one file a bad account switch can ruin
// and nothing else can reconstruct. It can also carry MCP server credentials
// (mcpServers.*.headers / .env are free-form passthrough), which is exactly why
// this stays a local .bak and is never what gets synced: the repo records only
// the three identity fields, via devices.Account.
//
// An absent file is not an error — a machine that has never logged in has
// nothing to protect.
func backupIdentityFile(out io.Writer) error {
	src, err := account.GlobalConfigPath()
	if err != nil {
		return nil // no home dir: nothing addressable to back up
	}
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // never logged in here: nothing to protect
		}
		// Anything else — unreadable, an I/O error — is not "no identity file".
		// Skipping silently would drop the one file this backup exists for, at
		// the moment its state is least certain.
		return fmt.Errorf("backup identity: %w", err)
	}
	dst := src + ".bak"
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("backup %s already exists; move it away first", dst)
	}
	fmt.Fprintf(out, "  backing up %s → %s\n", src, dst)
	if err := copyOne(src, dst); err != nil {
		return fmt.Errorf("backup identity: %w", err)
	}
	return nil
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
	// Copy the source's permissions, don't invent them. ~/.claude is full of
	// 0600 transcripts and the identity file beside it is 0600 too; os.Create
	// would widen every one of them to 0644 in the backup, so the act of
	// protecting this data would be what exposed it.
	mode := os.FileMode(0o600)
	if fi, serr := in.Stat(); serr == nil {
		mode = fi.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// OpenFile's mode only applies when it creates the file, and umask can clip
	// it even then — restate it so the backup matches the source exactly.
	return os.Chmod(dst, mode)
}
