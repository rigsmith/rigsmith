package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/codexrig/agentsmd"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/spf13/cobra"
)

// NewGuideCmd builds `codexrig guide`, which writes codexrig's managed blocks
// into AGENTS.md so an agent working in the repo knows the rules the guard
// enforces.
func NewGuideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Write codexrig's blocks into AGENTS.md",
		Long: "Codex reads AGENTS.md for instructions. These blocks tell it the branch\n" +
			"discipline the guard enforces and what codexrig backs up — so the prose and\n" +
			"the hook agree. An instruction file describing a rule that is not enforced is\n" +
			"worse than none, because it gets believed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	var global bool
	var path string
	add := func(c *cobra.Command) *cobra.Command {
		c.Flags().BoolVar(&global, "global", false, "write into your Codex home's AGENTS.md instead of this repository's")
		c.Flags().StringVar(&path, "path", "", "write into this file instead")
		return c
	}
	cmd.AddCommand(
		add(&cobra.Command{
			Use: "install", Short: "Add or refresh the blocks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := guidePath(cmd, path, global)
				if err != nil {
					return err
				}
				act, err := agentsmd.InstallAll(p)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render(string(act)), p)
				return nil
			},
		}),
		add(&cobra.Command{
			Use: "uninstall", Short: "Remove the blocks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := guidePath(cmd, path, global)
				if err != nil {
					return err
				}
				act, err := agentsmd.UninstallAll(p)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render(string(act)), p)
				return nil
			},
		}),
		add(&cobra.Command{
			Use: "status", Short: "Say whether the blocks are there", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := guidePath(cmd, path, global)
				if err != nil {
					return err
				}
				ok, err := agentsmd.AllPresent(p)
				if err != nil {
					return err
				}
				if ok {
					fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("present"), p)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", DimStyle.Render("not installed —"), p)
				return nil
			},
		}),
		&cobra.Command{
			Use: "show", Short: "Print the blocks without writing anything", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Fprint(cmd.OutOrStdout(), agentsmd.Blocks())
				return nil
			},
		},
	)
	return cmd
}

// guidePath picks the AGENTS.md to write: an explicit one, the Codex home's, or
// this repository's.
func guidePath(cmd *cobra.Command, explicit string, global bool) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if global {
		home, err := codexhome.Default()
		if err != nil {
			return "", err
		}
		return codexhome.Instructions(home), nil
	}
	if root, err := repoRoot(cmd.Context()); err == nil {
		return filepath.Join(root, "AGENTS.md"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, "AGENTS.md"), nil
}
