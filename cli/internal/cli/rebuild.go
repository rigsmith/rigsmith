package cli

import (
	"fmt"

	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newRebuildCmd is not a single native command, so it sequences clean → build.
// (rebuild is registered via verbCmd in root.go for the help listing; this is
// the actual runner, dispatched from there when verb == "rebuild".)
//
// Implemented as a special case in verbCmd rather than a DevCommands entry
// because "rebuild" has no single argv across ecosystems.
func runRebuild(cmd *cobra.Command, eco, root string, args []string) error {
	for _, verb := range []string{"clean", "build"} {
		argv, ok := detect.CommandFor(eco, verb, root)
		if !ok {
			if verb == "clean" {
				continue // some ecosystems have no clean; build alone is fine
			}
			return fmt.Errorf("verb %q has no mapping for ecosystem %q", verb, eco)
		}
		if verb == "build" {
			argv = append(argv, args...)
		}
		if err := runCommand(cmd, root, argv); err != nil {
			return err
		}
	}
	return nil
}
