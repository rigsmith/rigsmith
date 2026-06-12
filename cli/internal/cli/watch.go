package cli

import (
	"fmt"
	"os"

	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// newWatchCmd is the watch modifier: `rig watch <verb> [project]` (alias `w`)
// runs a dev verb in the ecosystem's watch mode. Verb shorthands resolve
// (`rig w r` → run). An optional project scopes it to that package's dir.
func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "watch <verb> [project]",
		Aliases: []string{"w"},
		Short:   "Run a dev verb in watch mode",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verb := normalizeWatchVerb(args[0])
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)

			// Optional project selector.
			dir, eco := root, ""
			if len(args) > 1 {
				ts := discoverWorkspace(cdContext(cmd), root)
				if t, ok := matchTarget(ts, args[1]); ok {
					dir, eco = t.Dir, t.Eco
				} else {
					return fmt.Errorf("no project matching %q", args[1])
				}
			}
			if eco == "" {
				resolved, err := resolvePrimary(cwd, root)
				if err != nil {
					return err
				}
				eco = resolved
			}

			argv, ok := watchCommandFor(eco, verb, root)
			if !ok {
				return fmt.Errorf("watch %q is not supported for ecosystem %q", verb, eco)
			}
			return runCommand(cmd, dir, argv)
		},
	}
}

// normalizeWatchVerb maps shorthands to canonical verbs.
func normalizeWatchVerb(s string) string {
	switch s {
	case "r", "run", "dev":
		return "run"
	case "b", "build":
		return "build"
	case "t", "test":
		return "test"
	case "fmt", "format":
		return "format"
	case "l", "lint":
		return "lint"
	case "tc", "check", "typecheck":
		return "typecheck"
	default:
		return s
	}
}

// watchCommandFor returns the watch-mode argv for verb in eco, or ok=false when
// the ecosystem has no native watch for it.
func watchCommandFor(eco, verb, root string) ([]string, bool) {
	switch eco {
	case detect.DotNet:
		switch verb {
		case "build", "test", "run":
			return []string{"dotnet", "watch", verb}, true
		}
	case detect.Cargo:
		// Requires cargo-watch; maps the verb to a cargo subcommand.
		return []string{"cargo", "watch", "-x", verb}, true
	case detect.Node:
		pm := string(detect.DetectNodePM(root))
		if verb == "run" {
			return []string{pm, "run", "dev"}, true // the dev script watches
		}
		// vitest/jest/tsc honor --watch; forward it to the script.
		if verb == "build" || verb == "test" || verb == "lint" || verb == "typecheck" {
			return []string{pm, "run", verb, "--", "--watch"}, true
		}
	case detect.Go:
		// Go has no native watch; `rig run`/`test` re-run is the closest.
		return nil, false
	}
	return nil, false
}
