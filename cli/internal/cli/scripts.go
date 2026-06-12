package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// isBuiltinVerb is the set of names rig owns, so custom commands and surfaced
// package.json scripts never shadow a built-in verb.
var isBuiltinVerb = map[string]bool{
	"build": true, "test": true, "run": true, "format": true, "lint": true,
	"typecheck": true, "rebuild": true, "install": true, "ci": true, "add": true,
	"uninstall": true, "outdated": true, "upgrade": true, "clean": true,
	"global": true, "dlx": true, "coverage": true, "kill": true, "doctor": true,
	"info": true, "ui": true, "cd": true, "init": true, "watch": true,
}

// scriptCmds surfaces every package.json script (in a Node repo) that isn't
// already a built-in verb as its own `rig <script>` subcommand — the parity to
// the Node rig's scripts→verbs. Each runs `<pm> run <script>` (package-manager
// detected) with any extra args forwarded.
func scriptCmds(root string) []*cobra.Command {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil || len(pkg.Scripts) == 0 {
		return nil
	}
	pm := string(detect.DetectNodePM(root))

	names := make([]string, 0, len(pkg.Scripts))
	for name := range pkg.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)

	var cmds []*cobra.Command
	for _, name := range names {
		if isBuiltinVerb[name] {
			continue // a built-in dev verb already maps this
		}
		script := name
		cmds = append(cmds, &cobra.Command{
			Use:                script,
			Short:              "Script: " + pkg.Scripts[script],
			GroupID:            "",
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(cmd *cobra.Command, args []string) error {
				argv := append([]string{pm, "run", script}, args...)
				return runCommand(cmd, root, argv)
			},
		})
	}
	return cmds
}
