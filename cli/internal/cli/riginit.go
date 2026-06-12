package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// defaultRigJSON is the scaffold written by `rig init`. Plain JSON (rig's loader
// reads JSON, not JSONC), with every optional key shown.
const defaultRigJSON = `{
  "$schema": "https://rigsmith.dev/schemas/rig.json",
  "defaultProject": "",
  "ecosystem": "",
  "quiet": false,
  "exclude": [],
  "env": {},
  "commands": {},
  "kill": { "match": [] }
}
`

// newRigInitCmd scaffolds a .rig.json at the repo root. It refuses to overwrite.
func newRigInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold a .rig.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)
			path := filepath.Join(root, config.FileName)
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s already exists at %s\n", config.FileName, root)
				return nil
			}
			if err := os.WriteFile(path, []byte(defaultRigJSON), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", path)
			return nil
		},
	}
}
