package cli

import (
	"fmt"
	"os"
	"strings"

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
			return runWatchVerb(cmd, normalizeWatchVerb(args[0]), args[1:])
		},
	}
}

// runWatchVerb runs verb in the ecosystem's watch mode, optionally scoped to a
// project named by rest[0]; any further tokens are forwarded to the watch argv.
// Shared by the `watch` modifier subcommand and the per-verb --watch flag (the
// .NET rig's `run --watch` / `test --watch` at any flag position).
func runWatchVerb(cmd *cobra.Command, verb string, rest []string) error {
	cwd, _ := os.Getwd()
	root := detect.Root(cwd)

	// Optional project selector: a first token naming a package scopes the
	// watch to that package's directory.
	dir, eco, forwarded, matched := root, "", rest, false
	if len(rest) > 0 {
		ts := discoverWorkspace(cdContext(cmd), root)
		if t, ok := matchTarget(ts, rest[0]); ok {
			dir, eco, forwarded, matched = t.Dir, t.Eco, rest[1:], true
		}
	}
	if eco == "" {
		resolved, err := resolvePrimary(cwd, root)
		if err != nil {
			return err
		}
		eco = resolved
	}

	if len(rest) > 0 && !matched {
		// `rig test <class> --watch` in a .NET repo: the unmatched token is a
		// test-class query / filter shorthand, run under `dotnet watch test`.
		if verb == "test" && eco == detect.DotNet {
			return runDotnetTest(cmd, root, rest, true)
		}
		return fmt.Errorf("no project matching %q", rest[0])
	}

	argv, ok := watchCommandFor(eco, verb, root)
	if !ok {
		return fmt.Errorf("watch %q is not supported for ecosystem %q", verb, eco)
	}
	return runCommand(cmd, dir, append(argv, forwarded...))
}

// watchableVerb reports whether a dev verb carries a --watch flag, mirroring
// the .NET rig (RunCommand/BuildCommand/TestCommand declare `--watch -w`).
func watchableVerb(verb string) bool {
	switch verb {
	case "run", "build", "test":
		return true
	}
	return false
}

// expandWatch turns a leading `watch`/`w` modifier into a `--watch` flag on the
// target verb (the .NET rig's PrefixResolver.ExpandWatch): `rig watch run` /
// `rig w r` → `rig run --watch`. Execute then folds the flag back onto the
// `watch` subcommand, which owns the per-ecosystem watch mapping, so the verb
// still gets prefix resolution first. Bare `watch` → empty (falls through).
func expandWatch(args []string) []string {
	if len(args) == 0 {
		return args
	}
	if !strings.EqualFold(args[0], "watch") && !strings.EqualFold(args[0], "w") {
		return args
	}
	rest := args[1:]
	if len(rest) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(rest)+1)
	out = append(out, rest...)
	return append(out, "--watch")
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
