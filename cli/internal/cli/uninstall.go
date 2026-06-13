package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newUninstallCmd builds `rig uninstall <pkg>…` (aliases remove/rm). Most
// ecosystems have a native removal verb (`dotnet remove package`, `npm
// uninstall`, `cargo remove`) reached through the adapter's DevCommands. Go has
// none — dropping a dependency is `go get pkg@none` followed by `go mod tidy` —
// so it is special-cased here. A bare `rig uninstall` on Go just tidies (prunes
// unused modules).
func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall [packages...]",
		Short:   "Remove a dependency",
		Aliases: []string{"remove", "rm"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			eco, err := resolvePrimary(cwd, root)
			if err != nil {
				return err
			}
			if eco == detect.Go {
				return runGoUninstall(cmd, root, args)
			}
			argv, ok := detect.CommandFor(eco, "uninstall", root)
			if !ok {
				return fmt.Errorf("verb %q has no mapping for ecosystem %q yet", "uninstall", eco)
			}
			return runCommand(cmd, root, append(argv, args...))
		},
	}
}

// runGoUninstall removes the named modules with `go get pkg@none`, then runs
// `go mod tidy`. With no package named it only tidies (prunes unused modules).
func runGoUninstall(cmd *cobra.Command, root string, args []string) error {
	if len(args) > 0 {
		get := []string{"go", "get"}
		for _, a := range args {
			get = append(get, goRemovalSpec(a))
		}
		if err := runCommand(cmd, root, get); err != nil {
			return err
		}
	}
	return runCommand(cmd, root, []string{"go", "mod", "tidy"})
}

// goRemovalSpec normalizes a module path to the `pkg@none` form `go get` uses to
// drop a dependency, leaving an explicit `@version`/`@none` the caller already
// wrote untouched. Pure.
func goRemovalSpec(arg string) string {
	if strings.Contains(arg, "@") {
		return arg
	}
	return arg + "@none"
}
