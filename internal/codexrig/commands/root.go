// Package commands builds the independent codexrig command tree.
package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rigsmith/rigsmith/internal/codexrig/adapter"
	"github.com/spf13/cobra"
)

// NewRootCmd does not resolve homes or inspect files until inspect is invoked.
func NewRootCmd(version string) *cobra.Command {
	return newRootCmd(version, os.UserHomeDir, os.Getenv)
}

func newRootCmd(version string, home func() (string, error), getenv func(string) string) *cobra.Command {
	root := &cobra.Command{
		Use: "codexrig", Version: version,
		Short: "Inspect Codex portability candidates (v2 preview)",
		Long: "codexrig is the separate Codex frontend for rigsmith's shared backup infrastructure.\n" +
			"This preview provides read-only source inspection. Sync, restore and queued\n" +
			"Codex hooks are not implemented yet. It does not change ClaudeRig or Codex state.",
		SilenceUsage: true,
	}
	var codexHome, skillsDir string
	var asJSON bool
	inspect := &cobra.Command{
		Use: "inspect", Short: "List configuration and customization candidates without reading their contents",
		Long: "Inspect the Codex home and shared user-skills directory using Codex-specific\n" +
			"selection rules. Lists regular-file candidates and the handling each still needs\n" +
			"before backup; this is not a secret scan or a complete Codex state export.\n" +
			"Known credential files, sessions, databases and downloaded plugins are\n" +
			"excluded. Linked files/directories are not selected. Missing roots are reported.\n" +
			"Default roots: CODEX_HOME (or ~/.codex) and ~/.agents/skills. Overrides must\n" +
			"be absolute directories. No content, configuration or recovery state is written.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			userHome, err := home()
			if err != nil {
				return err
			}
			source := codexHome
			if !c.Flags().Changed("codex-home") {
				source = getenv("CODEX_HOME")
			}
			if c.Flags().Changed("codex-home") && source == "" || c.Flags().Changed("skills-dir") && skillsDir == "" {
				return fmt.Errorf("directory overrides must not be empty")
			}
			roots, err := adapter.Roots(userHome, source, skillsDir)
			if err != nil {
				return err
			}
			reports := make([]adapter.Inventory, 0, len(roots))
			for _, r := range roots {
				report, err := adapter.Inspect(c.Context(), r)
				if err != nil {
					return err
				}
				reports = append(reports, report)
			}
			if asJSON {
				return json.NewEncoder(c.OutOrStdout()).Encode(struct {
					Version int                 `json:"version"`
					Mode    string              `json:"mode"`
					Roots   []adapter.Inventory `json:"roots"`
				}{1, "inventory-only", reports})
			}
			if _, err := fmt.Fprintln(c.OutOrStdout(), "Inventory only; candidates still require capture policy and secret checks."); err != nil {
				return err
			}
			for _, report := range reports {
				if _, err := fmt.Fprintf(c.OutOrStdout(), "%s %q (present: %t)\n", report.Root, report.Path, report.Present); err != nil {
					return err
				}
				for _, f := range report.Candidates {
					if _, err := fmt.Fprintf(c.OutOrStdout(), "  %s %q — requires %s\n", f.Kind, f.Path, f.Requires); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	inspect.Flags().StringVar(&codexHome, "codex-home", "", "Codex source directory (overrides CODEX_HOME)")
	inspect.Flags().StringVar(&skillsDir, "skills-dir", "", "Shared user-skills directory (default ~/.agents/skills)")
	inspect.Flags().BoolVar(&asJSON, "json", false, "Emit the versioned inventory as JSON")
	_ = inspect.MarkFlagDirname("codex-home")
	_ = inspect.MarkFlagDirname("skills-dir")
	root.AddCommand(inspect)
	return root
}
