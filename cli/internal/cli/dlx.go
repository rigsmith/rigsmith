package cli

import (
	"fmt"
	"os"

	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newDlxCmd builds `rig dlx <tool> [args…]` (alias `x`) — run a tool once
// without installing it. Each ecosystem maps it natively (`dnx`, `npx`/`bun x`/
// `pnpm dlx`, `go run pkg@latest`). On .NET it preflights `dnx` (which ships
// with the .NET 10 SDK) so a missing runtime gives clear guidance instead of a
// raw "command not found".
func newDlxCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "dlx [tool] [args...]",
		Short:   "Run a tool once without installing",
		Aliases: []string{"x"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			argv, ok := detect.CommandFor(eco, "dlx", root)
			if !ok {
				return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", "dlx", eco)
			}
			if eco == detect.DotNet {
				if _, err := toolDnx.require(cmd, root); err != nil {
					return err
				}
			}
			return runCommand(cmd, root, append(argv, args...))
		},
	}
}
