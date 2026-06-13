package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/clauderig/internal/claudemd"
	"github.com/spf13/cobra"
)

// NewGuideCmd builds the `guide` command group: install/remove clauderig's
// worktree-discipline instructions as a managed block in a CLAUDE.md, so every
// Claude context in the repo learns the rules the `clauderig guard` hook enforces.
//
// The block is fenced by markers and re-installed in place, so it never disturbs
// the rest of the file and always reflects the installed clauderig version.
func NewGuideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Install/remove clauderig's worktree-discipline block in CLAUDE.md",
		Long: "Manage a marker-delimited block in CLAUDE.md that explains the worktree/PR\n" +
			"rules `clauderig guard` enforces. Defaults to the repo's CLAUDE.md; use\n" +
			"--global for ~/.claude/CLAUDE.md (applies to every project).",
	}
	var global bool
	var path string
	persist := func(c *cobra.Command) {
		c.Flags().BoolVar(&global, "global", false, "target ~/.claude/CLAUDE.md instead of the repo's")
		c.Flags().StringVar(&path, "path", "", "explicit CLAUDE.md path (overrides --global and repo detection)")
	}
	resolve := func() (string, error) { return guidePath(path, global) }

	install := &cobra.Command{
		Use:   "install",
		Short: "Add (or update) the worktree-discipline block in CLAUDE.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			act, err := claudemd.Install(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(p))
			return nil
		},
	}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the worktree-discipline block from CLAUDE.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			act, err := claudemd.Uninstall(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(p))
			return nil
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Report whether CLAUDE.md carries the block",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			present, err := claudemd.Present(p)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if present {
				fmt.Fprintf(out, "%s installed in %s\n", OkStyle.Render("✓"), p)
			} else {
				fmt.Fprintf(out, "%s not installed in %s\n", DimStyle.Render("—"), p)
			}
			return nil
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the managed block to stdout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprint(cmd.OutOrStdout(), claudemd.Block())
			return nil
		},
	}
	for _, c := range []*cobra.Command{install, uninstall, status} {
		persist(c)
	}
	cmd.AddCommand(install, uninstall, status, show)
	return cmd
}

// guidePath picks the CLAUDE.md to manage: an explicit --path wins, then --global
// (~/.claude/CLAUDE.md), else the repo root's CLAUDE.md (falling back to the
// current directory when not inside a repo).
func guidePath(explicit string, global bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "CLAUDE.md"), nil
	}
	if _, root, err := openRepo(context.Background()); err == nil {
		return filepath.Join(root, "CLAUDE.md"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "CLAUDE.md"), nil
}
