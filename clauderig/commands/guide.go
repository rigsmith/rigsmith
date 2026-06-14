package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/clauderig/internal/claudemd"
	"github.com/rigsmith/clauderig/internal/tui"
	"github.com/spf13/cobra"
)

// NewGuideCmd builds the `guide` command group: install/remove clauderig's
// managed instruction blocks in a CLAUDE.md, so every Claude context in the repo
// learns the worktree/PR rules the `clauderig guard` hook enforces and how to use
// the rigsmith CLI family (rig / changerig / shiprig).
//
// Each block is fenced by markers and re-installed in place, so it never disturbs
// the rest of the file and always reflects the installed clauderig version.
func NewGuideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Install/remove clauderig's managed blocks in CLAUDE.md",
		Long: "Manage marker-delimited blocks in CLAUDE.md: the worktree/PR rules\n" +
			"`clauderig guard` enforces, and how to use the rigsmith tools (rig /\n" +
			"changerig / shiprig). Defaults to the repo's CLAUDE.md; use --global for\n" +
			"~/.claude/CLAUDE.md (applies to every project).",
	}
	var global bool
	var path string
	var yes bool
	persist := func(c *cobra.Command) {
		c.Flags().BoolVar(&global, "global", false, "target ~/.claude/CLAUDE.md instead of the repo's")
		c.Flags().StringVar(&path, "path", "", "explicit CLAUDE.md path (overrides --global and repo detection)")
	}
	resolve := func() (string, error) { return guidePath(path, global) }

	install := &cobra.Command{
		Use:   "install",
		Short: "Add (or update) clauderig's blocks in CLAUDE.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			// Let the user see exactly what will be written before it lands.
			// Skipped with --yes and when there's no terminal (hooks/CI), which
			// keeps install scriptable.
			if !yes && interactive() {
				ok, err := previewBlocks(p)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("cancelled — nothing written"))
					return nil
				}
			}
			out := cmd.OutOrStdout()
			for _, sec := range claudemd.Sections {
				act, err := sec.Install(p)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(sec.Begin))
			}
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(p))
			return nil
		},
	}
	install.Flags().BoolVarP(&yes, "yes", "y", false, "skip the interactive preview and write immediately")
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove clauderig's blocks from CLAUDE.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, sec := range claudemd.Sections {
				act, err := sec.Uninstall(p)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s %s %s\n", OkStyle.Render("✓"), act, DimStyle.Render(sec.Begin))
			}
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(p))
			return nil
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Report which blocks CLAUDE.md carries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, sec := range claudemd.Sections {
				present, err := sec.Present(p)
				if err != nil {
					return err
				}
				if present {
					fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ installed"), DimStyle.Render(sec.Begin))
				} else {
					fmt.Fprintf(out, "%s %s\n", DimStyle.Render("— absent   "), DimStyle.Render(sec.Begin))
				}
			}
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(p))
			return nil
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the managed blocks to stdout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprint(cmd.OutOrStdout(), allBlocks())
			return nil
		},
	}
	for _, c := range []*cobra.Command{install, uninstall, status} {
		persist(c)
	}
	cmd.AddCommand(install, uninstall, status, show)
	return cmd
}

// allBlocks is every managed block joined as it would appear in CLAUDE.md — a
// blank line between blocks, mirroring how Install separates them.
func allBlocks() string {
	parts := make([]string, 0, len(claudemd.Sections))
	for _, sec := range claudemd.Sections {
		parts = append(parts, sec.Block())
	}
	return strings.Join(parts, "\n")
}

// previewBlocks runs the scrollable preview over everything install would write
// and returns the user's decision. It's only called on a TTY.
func previewBlocks(path string) (bool, error) {
	m := tui.NewPreview(
		"clauderig will add these blocks to CLAUDE.md",
		"→ "+path,
		allBlocks(),
	)
	res, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return false, err
	}
	final, ok := res.(tui.Preview)
	if !ok {
		return false, nil
	}
	return final.Confirmed, nil
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
