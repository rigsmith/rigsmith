package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultConfigJSON = `{
  "$schema": "https://rigsmith.dev/schemas/changeset-config.json",
  "baseBranch": "main",
  "access": "restricted",
  "updateInternalDependencies": "patch",
  "ignore": [],
  "linked": [],
  "fixed": []
}
`

const changesetReadme = `# Changesets

This folder holds changesets — intent files describing pending releases. Add one
with ` + "`changerig add`" + `; consume them with ` + "`changerig version`" + `.
`

// NewInitCmd builds the `init` command.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the .changeset folder and config",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := Open()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(ws.ChangesetDir, 0o755); err != nil {
				return err
			}
			cfgPath := filepath.Join(ws.ChangesetDir, "config.json")
			if _, err := os.Stat(cfgPath); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Already initialized at %s\n", ws.ChangesetDir)
				return nil
			}
			if err := os.WriteFile(cfgPath, []byte(defaultConfigJSON), 0o644); err != nil {
				return err
			}
			readmePath := filepath.Join(ws.ChangesetDir, "README.md")
			if _, err := os.Stat(readmePath); os.IsNotExist(err) {
				_ = os.WriteFile(readmePath, []byte(changesetReadme), 0o644)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized changesets in %s\n", ws.ChangesetDir)
			return nil
		},
	}
}
