package cli

import (
	"errors"
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

// setDefaultProject validates query against the repo's runnable projects and
// persists `defaultProject` in the repo's .rig.json via the comment-preserving
// config writer, returning the config path written. It writes nothing when the
// query matches no runnable project (error) or several (ambiguous — the caller
// decides). Port of the .NET rig's DefaultVerb set path; the full verb
// (interactive picker, current-value display) lives in defaultverb.go.
func setDefaultProject(root string, cfg config.Config, query string) (string, error) {
	projects := detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	res := resolveRunProject(projects, query, "")
	if res.Err != "" {
		return "", errors.New(res.Err)
	}
	if res.Selected == nil {
		return "", fmt.Errorf("%q matches multiple projects", query)
	}
	return config.SetRepoString(root, "defaultProject", res.Selected.Name), nil
}

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
