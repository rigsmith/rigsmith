package commands

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
				// Preflight BOTH destinations before copying EITHER. Checking the
				// identity path only after the tree was copied meant an existing
				// ~/.claude.json.bak aborted the restore having already written a
				// half of the pair — a rollback set that is not a set.
				if err := backupPathIsFree(bak); err != nil {
					return err
				}
				idSrc, idDst, hasIdentity, idNote, ierr := identityBackupPaths()
				if ierr != nil {
					return ierr
				}
				if idNote != "" {
					fmt.Fprintf(out, "  %s\n", WarnStyle.Render("⚠ "+idNote))
				}
				if hasIdentity {
					if err := backupPathIsFree(idDst); err != nil {
						return err
					}
				}
				fmt.Fprintf(out, "  backing up %s → %s\n", cliTarget, bak)
				if err := copyTree(cliTarget, bak); err != nil {
					return fmt.Errorf("backup: %w", err)
				}
				if hasIdentity {
					fmt.Fprintf(out, "  backing up %s → %s\n", idSrc, idDst)
					// 0600 rather than the source's mode. copyOne preserves
					// permissions, which is right for transcripts — but this file
					// can carry MCP server credentials in mcpServers.*.headers,
					// and a source someone left world-readable would otherwise be
					// duplicated into a second world-readable copy they never
					// knew they were creating.
					//
					// On Unix that is a real narrowing. On Windows it is not:
					// see copyOneAs. The backup is no more exposed than the file
					// it came from on either platform, but only one of them is
					// actually being protected here.
					if err := copyOneAs(idSrc, idDst, 0o600); err != nil {
						return fmt.Errorf("backup identity: %w", err)
					}
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
				if r.Conflicts > 0 {
					extra += fmt.Sprintf(", %d skipped (a directory holds that path)", r.Conflicts)
				}
				if r.LinksKept > 0 {
					extra += fmt.Sprintf(", %d kept under existing link(s)", r.LinksKept)
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
	// Resolve the ROOT once. If ~/.claude is itself a symlink, WalkDir visits
	// only that entry and the branch below would recreate it — making .bak a
	// second link to the very directory restore is about to modify, so the
	// "backup" would track the changes instead of preserving what came before.
	// Links found BELOW the root are still reproduced, not followed.
	root, rerr := filepath.EvalSymlinks(src)
	if rerr != nil {
		root = src // unreadable or absent: let WalkDir report it
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return copyLink(p, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyOneInto(dst, p, target)
	})
}

// noSymlinkBetween reports an error when any directory from root down to the
// parent of dst is a symlink — the ancestors an O_EXCL create would still
// follow out of the tree.
func noSymlinkBetween(root, dst string) error {
	dir := filepath.Dir(dst)
	for {
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
		fi, lerr := os.Lstat(dir)
		if lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through a symlinked directory inside the backup: %s", dir)
		}
		dir = filepath.Dir(dir)
	}
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
// identityBackupPaths reports where ~/.claude.json would be backed up to, and
// whether there is one to back up.
//
// It holds the oauthAccount block — the ONLY record of which account this
// machine is logged in as — and it sits outside ~/.claude, so the tree copy
// misses it entirely. That is the one file a bad account switch can ruin and
// nothing else can reconstruct. It can also carry MCP server credentials
// (mcpServers.*.headers / .env are free-form passthrough), which is why this
// stays a local .bak and is never what gets synced.
//
// Absent is not an error — a machine that has never logged in has nothing to
// protect — but unreadable is, because that is the moment its state is least
// certain and least replaceable.
func identityBackupPaths() (src, dst string, exists bool, note string, err error) {
	src, herr := account.GlobalConfigPath()
	if herr != nil {
		return "", "", false, "", nil // no home dir: nothing addressable to back up
	}
	if _, serr := os.Stat(src); serr != nil {
		if !errors.Is(serr, os.ErrNotExist) {
			return "", "", false, "", fmt.Errorf("backup identity: %w", serr)
		}
		// Stat FOLLOWS links, so a dangling symlink lands here too and looks
		// exactly like "never logged in". There genuinely is nothing to copy,
		// but the two states mean opposite things to someone about to restore
		// — one is a machine with no identity, the other is an identity whose
		// target has gone missing. Lstat separates them so the second can be
		// said out loud instead of passing as the first.
		if fi, lerr := os.Lstat(src); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(src)
			return "", "", false, fmt.Sprintf(
				"%s is a symlink to %s, which does not exist — no identity was backed up", src, target), nil
		}
		return "", "", false, "", nil
	}
	return src, src + ".bak", true, "", nil
}

// backupPathIsFree reports that nothing occupies the backup path yet.
//
// Lstat, not Stat. A DANGLING symlink there passes a Stat check as "not
// present", and the copy would then follow it — writing the backup through the
// link to wherever it points. For the identity file that means ~/.claude.json,
// which can carry MCP server credentials, landing somewhere never intended. Any
// existing entry, link included, is a refusal, and a lookup that fails for any
// other reason stops the backup rather than guessing.
func backupPathIsFree(path string) error {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return fmt.Errorf("backup %s already exists; move it away first", path)
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("check backup path %s: %w", path, err)
	}
}

// copyOne copies one file to a destination that must not already exist.
//
// root, when non-empty, is the tree dst must stay inside. O_EXCL protects only
// the LEAF: replacing an intermediate directory with a symlink after MkdirAll
// still redirects the create, so the file lands outside the backup entirely.
// Revalidating the ancestors narrows that window — it cannot close it without
// openat/O_NOFOLLOW per component, which Go does not offer portably, and the
// alternative of not checking at all is strictly worse.
func copyOne(src, dst string) error { return copyOneInto("", src, dst) }

// copyOneAs copies with an explicit destination mode instead of the source's,
// for a file whose permissions should not be inherited from wherever it came.
//
// UNIX ONLY, and worth stating rather than implying: Go maps FileMode to the
// read-only attribute on Windows and leaves the ACL alone, so this narrows
// nothing there. A backup written beside ~/.claude.json inherits that
// directory's ACL either way — usually the user profile's, which is already
// restricted to the user — so the practical exposure on Windows is the same as
// the original file's. What this cannot do is IMPROVE on it.
func copyOneAs(src, dst string, mode os.FileMode) error {
	return copyOneWith("", src, dst, &mode)
}

func copyOneInto(root, src, dst string) error { return copyOneWith(root, src, dst, nil) }

func copyOneWith(root, src, dst string, force *os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if root != "" {
		if err := noSymlinkBetween(root, dst); err != nil {
			return err
		}
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
	if force != nil {
		mode = *force
	}
	// O_EXCL, and no O_TRUNC. backupPathIsFree checked that nothing occupied
	// this path, but that check and this open are two moments: a symlink
	// created in between would otherwise be FOLLOWED here and its target
	// overwritten — for the identity file, one that can carry MCP credentials.
	// O_CREATE|O_EXCL fails on any existing entry, a dangling symlink included,
	// which closes the window instead of narrowing it. Every destination is a
	// path nothing should hold yet, so nothing legitimate is refused.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// Through the DESCRIPTOR, not the path. OpenFile's mode only applies at
	// creation and umask can clip it even then, so it has to be restated — but
	// a path-based Chmod would reopen by name the race O_EXCL just closed,
	// letting the backup be renamed and a symlink planted in between so the
	// mode landed on something else entirely.
	return out.Chmod(mode)
}
