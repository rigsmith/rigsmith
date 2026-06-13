package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/rigsmith/clauderig/internal/settings"
	"github.com/spf13/cobra"
)

// NewHooksCmd builds the `hooks` command group — install/remove/inspect
// clauderig's Claude Code hooks. By default each hook lands at its natural scope:
// the sync hooks (SessionStart→pull, Stop→sync) at user scope (they ride
// clauderig's ~/.claude sync), and the guard (PreToolUse) at project scope (it
// rides the repo's .claude/settings.json). --scope forces a single target.
func NewHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install/remove clauderig's Claude Code hooks (sync at user scope, guard at project scope)",
	}

	install := &cobra.Command{
		Use:   "install",
		Short: "Add clauderig hooks (sync→user, guard→project); idempotent",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runHooksInstall(cmd) },
	}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove clauderig hooks from every scope (or --scope)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runHooksUninstall(cmd) },
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show which scopes carry clauderig hooks",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runHooksStatus(cmd) },
	}
	for _, c := range []*cobra.Command{install, uninstall, status} {
		c.Flags().String("scope", "", "force a single scope: user | project | local")
	}
	cmd.AddCommand(install, uninstall, status)
	return cmd
}

// scopeFlag reads and validates --scope; "" means "use the default per-scope split".
func scopeFlag(cmd *cobra.Command) (settings.Scope, error) {
	v, _ := cmd.Flags().GetString("scope")
	if v == "" {
		return "", nil
	}
	return settings.Parse(v)
}

func runHooksInstall(cmd *cobra.Command) error {
	forced, err := scopeFlag(cmd)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	root := repoRootBestEffort(cmd.Context())
	out := cmd.OutOrStdout()

	// Group plans by the scope they'll be written to.
	groups := map[settings.Scope][]hooks.Plan{}
	if forced != "" {
		groups[forced] = hooks.DefaultPlans()
	} else {
		for _, p := range hooks.DefaultPlans() {
			sc := p.Scope
			if (sc == settings.Project || sc == settings.Local) && root == "" {
				sc = settings.User // not in a repo — fall back so install still works
				fmt.Fprintf(out, "%s not in a repo — installing %s at user scope\n", WarnStyle.Render("!"), p.Command)
			}
			groups[sc] = append(groups[sc], p)
		}
	}

	for _, sc := range settings.All {
		grp := groups[sc]
		if len(grp) == 0 {
			continue
		}
		path, err := sc.Path(home, root)
		if err != nil {
			return err
		}
		added, err := hooks.Install(path, grp)
		if err != nil {
			return err
		}
		if len(added) == 0 {
			fmt.Fprintf(out, "%s %s — already installed\n", DimStyle.Render("•"), sc.Label())
		} else {
			fmt.Fprintf(out, "%s %s: %s\n", OkStyle.Render("✓"), sc.Label(), strings.Join(added, ", "))
		}
	}
	if forced == settings.Project || (forced == "" && root != "") {
		fmt.Fprintln(out, DimStyle.Render("  the project hook lives in .claude/settings.json — commit it to share; Claude Code will ask to trust it"))
	}
	return nil
}

func runHooksUninstall(cmd *cobra.Command) error {
	forced, err := scopeFlag(cmd)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	root := repoRootBestEffort(cmd.Context())
	out := cmd.OutOrStdout()

	any := false
	for _, sc := range scopesToSweep(forced, root) {
		path, err := sc.Path(home, root)
		if err != nil {
			continue
		}
		removed, err := hooks.Uninstall(path)
		if err != nil {
			return err
		}
		if len(removed) > 0 {
			any = true
			fmt.Fprintf(out, "%s %s: removed %s\n", OkStyle.Render("✓"), sc.Label(), strings.Join(removed, ", "))
		}
	}
	if !any {
		fmt.Fprintln(out, DimStyle.Render("no clauderig hooks to remove"))
	}
	return nil
}

func runHooksStatus(cmd *cobra.Command) error {
	forced, err := scopeFlag(cmd)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	root := repoRootBestEffort(cmd.Context())
	out := cmd.OutOrStdout()

	for _, sc := range scopesToSweep(forced, root) {
		path, err := sc.Path(home, root)
		if err != nil {
			continue
		}
		present, err := hooks.Status(path)
		if err != nil {
			return err
		}
		if len(present) == 0 {
			fmt.Fprintf(out, "%s %s\n", DimStyle.Render("—"), sc.Label())
		} else {
			fmt.Fprintf(out, "%s %s: %s\n", OkStyle.Render("✓"), sc.Label(), strings.Join(present, ", "))
		}
	}
	return nil
}

// scopesToSweep is the set of scopes uninstall/status look at: the forced one, or
// all that apply here (project/local only when inside a repo).
func scopesToSweep(forced settings.Scope, root string) []settings.Scope {
	if forced != "" {
		return []settings.Scope{forced}
	}
	out := []settings.Scope{settings.User}
	if root != "" {
		out = append(out, settings.Project, settings.Local)
	}
	return out
}

// repoRootBestEffort returns the current repo's top-level, or "" when not in one.
func repoRootBestEffort(ctx context.Context) string {
	if _, root, err := openRepo(ctx); err == nil {
		return root
	}
	return ""
}

// settingsPath is the user-scope settings file (~/.claude/settings.json), used by
// the status dashboard and init's sync-hook install.
func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}
