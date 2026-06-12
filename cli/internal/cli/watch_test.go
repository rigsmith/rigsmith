// Tests for the watch plumbing: the per-verb --watch flag (the .NET rig
// declares `--watch -w` on run/build/test — Commands.cs) and the pure
// per-ecosystem watch argv mapping. Hermetic: argv building only, nothing
// spawns.
package cli

import (
	"testing"

	"github.com/rigsmith/cli/internal/detect"
)

func TestWatchableVerb_MatchesTheDotNetRigsWatchFlags(t *testing.T) {
	// C# parity: RunCommand, BuildCommand, TestCommand declare --watch.
	for _, verb := range []string{"run", "build", "test"} {
		if !watchableVerb(verb) {
			t.Fatalf("watchableVerb(%q) = false, want true", verb)
		}
	}
	for _, verb := range []string{"format", "lint", "typecheck", "clean", "rebuild"} {
		if watchableVerb(verb) {
			t.Fatalf("watchableVerb(%q) = true, want false", verb)
		}
	}
}

func TestDevVerbs_DeclareTheWatchFlagWhereTheCSharpRigDoes(t *testing.T) {
	for _, tt := range []struct {
		verb string
		want bool
	}{
		{"run", true}, {"build", true}, {"test", true},
		{"format", false}, {"lint", false}, {"clean", false},
	} {
		cmd := devVerbCmd(tt.verb, "", true)
		flag := cmd.Flags().Lookup("watch")
		if got := flag != nil; got != tt.want {
			t.Fatalf("%s: has --watch = %v, want %v", tt.verb, got, tt.want)
		}
		if tt.want && flag.Shorthand != "w" {
			t.Fatalf("%s: --watch shorthand = %q, want w", tt.verb, flag.Shorthand)
		}
	}
}

func TestDevVerbs_WatchFlagParsesAtAnyPosition(t *testing.T) {
	// The C# parser accepts `rig run --watch`, `rig run --watch App`,
	// `rig run App --watch` alike; cobra's flag interspersion gives the same.
	for _, argv := range [][]string{
		{"--watch", "App"},
		{"App", "--watch"},
		{"-w", "App"},
	} {
		cmd := devVerbCmd("run", "", false)
		if err := cmd.ParseFlags(argv); err != nil {
			t.Fatalf("ParseFlags(%v): %v", argv, err)
		}
		if on, _ := cmd.Flags().GetBool("watch"); !on {
			t.Fatalf("ParseFlags(%v): watch flag not set", argv)
		}
		if args := cmd.Flags().Args(); len(args) != 1 || args[0] != "App" {
			t.Fatalf("ParseFlags(%v): positional args = %v, want [App]", argv, args)
		}
	}
}

func TestWatchCommandFor_DotNetMapsToDotnetWatch(t *testing.T) {
	root := t.TempDir()
	for _, verb := range []string{"run", "build", "test"} {
		argv, ok := watchCommandFor(detect.DotNet, verb, root)
		if !ok {
			t.Fatalf("dotnet watch %s: not supported", verb)
		}
		eqSlice(t, argv, []string{"dotnet", "watch", verb})
	}
	if _, ok := watchCommandFor(detect.DotNet, "format", root); ok {
		t.Fatal("dotnet watch format should not be supported")
	}
}

func TestWatchCommandFor_CargoUsesCargoWatch(t *testing.T) {
	argv, ok := watchCommandFor(detect.Cargo, "test", t.TempDir())
	if !ok {
		t.Fatal("cargo watch test: not supported")
	}
	eqSlice(t, argv, []string{"cargo", "watch", "-x", "test"})
}

func TestWatchCommandFor_NodeRunUsesTheDevScript(t *testing.T) {
	root := t.TempDir() // no lockfile → npm
	argv, ok := watchCommandFor(detect.Node, "run", root)
	if !ok {
		t.Fatal("node watch run: not supported")
	}
	eqSlice(t, argv, []string{"npm", "run", "dev"})

	argv, ok = watchCommandFor(detect.Node, "test", root)
	if !ok {
		t.Fatal("node watch test: not supported")
	}
	eqSlice(t, argv, []string{"npm", "run", "test", "--", "--watch"})
}

func TestWatchCommandFor_GoHasNoNativeWatch(t *testing.T) {
	if _, ok := watchCommandFor(detect.Go, "run", t.TempDir()); ok {
		t.Fatal("go watch run should not be supported")
	}
}
