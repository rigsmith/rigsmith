package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/claudemd"
	"github.com/rigsmith/rigsmith/internal/clauderig/gitignore"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
	"github.com/spf13/cobra"
)

// The scope commands make the *scope the command* — `clauderig <scope> <action>`
// — so there are no --scope flags to type. Each scope installs what belongs at it:
//
//	clauderig global  install   # sync hooks at ~/.claude (alias: `hooks`)
//	clauderig project install   # guard + CLAUDE.md guide, committed to the repo
//	clauderig local   install   # guard + CLAUDE.md guide, gitignored to this checkout
//
// `install` / `uninstall` / `status` live under each. project/local are also the
// home for any future command that should affect just this checkout.

// scopeSpec describes one scope command: where it writes and what it sets up.
type scopeSpec struct {
	scope   settings.Scope
	use     string
	aliases []string
	short   string
	plans   []hooks.Plan // hooks installed at this scope
	guide   bool         // also manage the CLAUDE.md guide block
}

func scopeSpecs() []scopeSpec {
	return []scopeSpec{
		{settings.User, "global", []string{"hooks"},
			"Install/remove clauderig's global sync hooks (~/.claude/settings.json)",
			hooks.SyncPlans(), false},
		{settings.Project, "project", nil,
			"Set up worktree discipline for this repo — guard + CLAUDE.md, committed",
			hooks.GuardPlans(), true},
		{settings.Local, "local", nil,
			"Set up worktree discipline for this repo — guard + CLAUDE.md, gitignored",
			hooks.GuardPlans(), true},
	}
}

// ScopeCommands builds the global/project/local command groups.
func ScopeCommands() []*cobra.Command {
	var cmds []*cobra.Command
	for _, sp := range scopeSpecs() {
		cmds = append(cmds, newScopeCmd(sp))
	}
	return cmds
}

func newScopeCmd(sp scopeSpec) *cobra.Command {
	cmd := &cobra.Command{Use: sp.use, Aliases: sp.aliases, Short: sp.short}
	cmd.AddCommand(
		&cobra.Command{
			Use: "install", Short: "Install " + sp.use + "-scope setup (idempotent)", Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error { return scopeInstall(c, sp) },
		},
		&cobra.Command{
			Use: "uninstall", Short: "Remove clauderig's " + sp.use + "-scope setup", Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error { return scopeUninstall(c, sp) },
		},
		&cobra.Command{
			Use: "status", Short: "Show this scope's clauderig setup", Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error { return scopeStatus(c, sp) },
		},
	)
	return cmd
}

// scopePaths resolves the repo root, settings file, and CLAUDE.md (when the scope
// manages the guide) for a scope. A project/local scope outside a git repo fails
// with a clear message from settings.Scope.Path.
func scopePaths(ctx context.Context, sp scopeSpec) (root, settingsFile, guideFile string, err error) {
	home, _ := os.UserHomeDir()
	root = repoRootBestEffort(ctx)
	settingsFile, err = sp.scope.Path(home, root)
	if err != nil {
		return "", "", "", err
	}
	if sp.guide {
		guideFile = filepath.Join(root, "CLAUDE.md")
	}
	return root, settingsFile, guideFile, nil
}

func scopeInstall(c *cobra.Command, sp scopeSpec) error {
	out := c.OutOrStdout()
	root, settingsFile, guideFile, err := scopePaths(c.Context(), sp)
	if err != nil {
		return err
	}
	added, err := hooks.Install(settingsFile, sp.plans)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		fmt.Fprintf(out, "%s hooks already installed %s\n", DimStyle.Render("•"), DimStyle.Render(settingsFile))
	} else {
		fmt.Fprintf(out, "%s hooks: %s %s\n", OkStyle.Render("✓"), strings.Join(added, ", "), DimStyle.Render(settingsFile))
	}
	if sp.guide {
		act, err := claudemd.InstallAll(guideFile)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s guide %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(guideFile))
	}
	// A local settings file is personal — keep it out of version control.
	if sp.scope == settings.Local {
		act, err := ensureLocalIgnored(c.Context(), root)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s gitignore %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(filepath.Join(root, ".gitignore")))
	}
	if sp.scope == settings.Project {
		fmt.Fprintln(out, DimStyle.Render("  commit .claude/settings.json to share it; Claude Code will ask to trust the hook"))
	}
	return nil
}

// ensureLocalIgnored makes sure .claude/settings.local.json is gitignored, unless
// an existing pattern already covers it. Returns what it did for reporting.
func ensureLocalIgnored(ctx context.Context, root string) (string, error) {
	const entry = ".claude/settings.local.json"
	if repo, err := gitrepo.Open(ctx, root); err == nil && repo.IsIgnored(ctx, entry) {
		return "already ignored", nil
	}
	giPath := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(giPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	next, changed := gitignore.EnsureLine(string(b), entry)
	if !changed {
		return "already ignored", nil
	}
	if err := os.WriteFile(giPath, []byte(next), 0o644); err != nil {
		return "", err
	}
	return "added", nil
}

func scopeUninstall(c *cobra.Command, sp scopeSpec) error {
	out := c.OutOrStdout()
	_, settingsFile, guideFile, err := scopePaths(c.Context(), sp)
	if err != nil {
		return err
	}
	removed, err := hooks.Uninstall(settingsFile)
	if err != nil {
		return err
	}
	if len(removed) > 0 {
		fmt.Fprintf(out, "%s hooks: removed %s %s\n", OkStyle.Render("✓"), strings.Join(removed, ", "), DimStyle.Render(settingsFile))
	} else {
		fmt.Fprintf(out, "%s no hooks %s\n", DimStyle.Render("•"), DimStyle.Render(settingsFile))
	}
	if sp.guide {
		act, err := claudemd.UninstallAll(guideFile)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s guide %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(guideFile))
	}
	return nil
}

func scopeStatus(c *cobra.Command, sp scopeSpec) error {
	out := c.OutOrStdout()
	_, settingsFile, guideFile, err := scopePaths(c.Context(), sp)
	if err != nil {
		return err
	}
	present, err := hooks.Status(settingsFile)
	if err != nil {
		return err
	}
	if len(present) > 0 {
		fmt.Fprintf(out, "%s hooks: %s %s\n", OkStyle.Render("✓"), strings.Join(present, ", "), DimStyle.Render(settingsFile))
	} else {
		fmt.Fprintf(out, "%s no hooks %s\n", DimStyle.Render("—"), DimStyle.Render(settingsFile))
	}
	if sp.guide {
		ok, err := claudemd.AllPresent(guideFile)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(out, "%s guide present %s\n", OkStyle.Render("✓"), DimStyle.Render(guideFile))
		} else {
			fmt.Fprintf(out, "%s guide absent %s\n", DimStyle.Render("—"), DimStyle.Render(guideFile))
		}
	}
	return nil
}
