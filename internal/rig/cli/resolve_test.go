// Project resolution: the rules that decide WHICH project a name (or a
// configured defaultProject) refers to. The regression these cover: `rig run
// Tweed.App` refused an ambiguous name while a bare `rig run` with
// defaultProject: "Tweed.App" silently launched the first match — a stale copy
// in a nested worktree that looks exactly like the real app.
package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
)

// exeCsproj is a runnable .NET project — the shape discovery classifies as a
// run target.
const exeCsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><OutputType>Exe</OutputType><TargetFramework>net8.0</TargetFramework></PropertyGroup>
</Project>`

// markLinkedWorktree writes the marker git puts at the root of a linked
// worktree: a `.git` FILE pointing at the parent repo's per-worktree admin dir.
func markLinkedWorktree(t *testing.T, root, rel string) {
	t.Helper()
	writeTreeFile(t, root, rel+"/.git", "gitdir: "+filepath.ToSlash(root)+"/.git/worktrees/"+filepath.Base(rel)+"\n")
}

// withIncludeWorktrees flips the --include-worktrees global for one test.
func withIncludeWorktrees(t *testing.T, v bool) {
	t.Helper()
	prev := includeWorktrees
	includeWorktrees = v
	t.Cleanup(func() { includeWorktrees = prev })
}

// ---------------------------------------------------------------------------
// Tiering: exact beats prefix beats substring, and the best tier is the answer.
// ---------------------------------------------------------------------------

func TestMatchTargets_ExactBeatsPrefixAndSubstring(t *testing.T) {
	targets := []target{
		{Name: "Tweed.App", Dir: "/r/ui/src/Tweed.App"},
		{Name: "Tweed.App.Tests", Dir: "/r/ui/tests/Tweed.App.Tests"},
		{Name: "Tweed.AcpAgent.Sample", Dir: "/r/agents/Tweed.AcpAgent.Sample"},
		{Name: "Legacy.Tweed.App.Shim", Dir: "/r/legacy/shim"},
	}
	// One exact hit is never ambiguous, however many looser matches exist.
	if m := matchTargets(targets, "Tweed.App"); len(m) != 1 || m[0].Name != "Tweed.App" {
		t.Fatalf("exact = %v, want only Tweed.App", names(m))
	}
	// No exact match → the prefix tier answers, and the substring match
	// (Legacy.Tweed.App.Shim) does not dilute it.
	if m := matchTargets(targets, "Tweed.App."); len(m) != 1 || m[0].Name != "Tweed.App.Tests" {
		t.Fatalf("prefix = %v, want only Tweed.App.Tests", names(m))
	}
	// Nothing exact or prefixed → substring.
	if m := matchTargets(targets, "AcpAgent"); len(m) != 1 || m[0].Name != "Tweed.AcpAgent.Sample" {
		t.Fatalf("substring = %v, want Tweed.AcpAgent.Sample", names(m))
	}
	// Subsequence stays out of reach: `rig test <class>` args must fall through
	// to the test-filter path rather than being read as a project name.
	if m := matchTargets(targets, "tas"); len(m) != 0 {
		t.Fatalf("subsequence = %v, want no match", names(m))
	}
}

// ---------------------------------------------------------------------------
// Proximity: the copy you are standing in wins the tie.
// ---------------------------------------------------------------------------

func TestNearestTargets_PrefersEnclosingThenNearestAncestor(t *testing.T) {
	here := target{Name: "App", Dir: filepath.FromSlash("/r/ui/src/App")}
	there := target{Name: "App", Dir: filepath.FromSlash("/r/wt/x/ui/src/App")}

	// Standing inside one copy selects it.
	got := nearestTargets(filepath.FromSlash("/r/ui/src/App/Views"), []target{there, here})
	if len(got) != 1 || got[0].Dir != here.Dir {
		t.Fatalf("enclosing = %v, want the copy containing cwd", dirs(got))
	}
	// Standing elsewhere in the same subtree: nearest common ancestor wins.
	got = nearestTargets(filepath.FromSlash("/r/ui/src/Other"), []target{there, here})
	if len(got) != 1 || got[0].Dir != here.Dir {
		t.Fatalf("nearest ancestor = %v, want the ui/src copy", dirs(got))
	}
	// At the repo root nothing distinguishes them — stay ambiguous rather than
	// guess, which is the whole point of the ambiguity error.
	if got = nearestTargets(filepath.FromSlash("/r"), []target{there, here}); len(got) != 2 {
		t.Fatalf("root cwd = %v, want both (ambiguous)", dirs(got))
	}
}

func dirs(ts []target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Dir
	}
	return out
}

// ---------------------------------------------------------------------------
// Nested worktrees: skipped by default, opt back in with --include-worktrees.
// ---------------------------------------------------------------------------

func TestDiscoverWorkspace_SkipsNestedWorktrees(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeTreeFile(t, root, "ui/src/App/App.csproj", exeCsproj)
	writeTreeFile(t, root, ".claude/worktrees/loom/ui/src/App/App.csproj", exeCsproj)
	markLinkedWorktree(t, root, ".claude/worktrees/loom")

	ts := discoverWorkspace(context.Background(), root, nil)
	if len(ts) != 1 {
		t.Fatalf("discovered %v, want only the primary checkout's App", dirs(ts))
	}
	if rel := relSlash(root, ts[0].Dir); rel != "ui/src/App" {
		t.Fatalf("discovered %q, want ui/src/App", rel)
	}

	// --include-worktrees brings the copy back for a cross-worktree sweep.
	withIncludeWorktrees(t, true)
	if ts := discoverWorkspace(context.Background(), root, nil); len(ts) != 2 {
		t.Fatalf("with --include-worktrees: discovered %v, want both copies", dirs(ts))
	}
}

// A submodule also carries a `.git` file — but it points into modules/, not
// worktrees/, and its projects must stay discoverable.
func TestDiscoverWorkspace_KeepsSubmodules(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeTreeFile(t, root, "vendored/Lib/Lib.csproj", exeCsproj)
	writeTreeFile(t, root, "vendored/.git", "gitdir: "+filepath.ToSlash(root)+"/.git/modules/vendored\n")

	if ts := discoverWorkspace(context.Background(), root, nil); len(ts) != 1 {
		t.Fatalf("discovered %v, want the submodule's project", dirs(ts))
	}
}

func TestRunTargets_SkipsGoBinariesInNestedWorktrees(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeTreeFile(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeTreeFile(t, root, "cmd/api/main.go", "package main\n\nfunc main() {}\n")
	writeTreeFile(t, root, "wt/x/cmd/api/main.go", "package main\n\nfunc main() {}\n")
	markLinkedWorktree(t, root, "wt/x")

	ts := runTargets(context.Background(), root)
	if len(ts) != 1 || relSlash(root, ts[0].Dir) != "cmd/api" {
		t.Fatalf("run targets = %v, want only cmd/api", dirs(ts))
	}
}

// ---------------------------------------------------------------------------
// The headline bug: implicit and explicit resolution must agree.
// ---------------------------------------------------------------------------

func TestOfferRunChoice_AmbiguousDefaultRefusesToGuess(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	primary := filepath.Join(root, filepath.FromSlash("ui/src/Tweed.App"))
	shadow := filepath.Join(root, filepath.FromSlash(".claude/worktrees/loom/ui/src/Tweed.App"))
	markLinkedWorktree(t, root, ".claude/worktrees/loom")
	tasks := []allTask{
		{name: "Tweed.App", dir: primary, rel: "ui/src/Tweed.App", argv: []string{"dotnet", "run"}},
		{name: "Tweed.App", dir: shadow, rel: ".claude/worktrees/loom/ui/src/Tweed.App", argv: []string{"dotnet", "run"}},
	}

	defer func(p bool) { dryRun = p }(dryRun)
	dryRun = true

	cmd, buf := newRunHost()
	_, err := offerRunChoice(cmd, root, tasks, nil, "Tweed.App", false)
	if err == nil {
		t.Fatalf("ambiguous default ran something instead of reporting it: %q", buf.String())
	}
	// Same shape of failure as `rig run Tweed.App`, and it names the default as
	// the source so the fix is obvious.
	if !strings.Contains(err.Error(), "matches 2 projects") || !strings.Contains(err.Error(), "defaultProject") {
		t.Fatalf("err = %v, want the same ambiguity report the explicit path gives", err)
	}
	out := buf.String()
	for _, want := range []string{"ui/src/Tweed.App", ".claude/worktrees/loom/ui/src/Tweed.App"} {
		if !strings.Contains(out, want) {
			t.Fatalf("listing = %q, want it to name %s", out, want)
		}
	}
	// The remedy is printed as a line you can paste, with a PATH glob — every
	// existing exclude value is a bare name, so the path form has to be shown.
	if !strings.Contains(out, `"exclude": [".claude/worktrees/loom/*"]`) {
		t.Fatalf("listing = %q, want a copy-pasteable exclude line", out)
	}
	// And it says what the extra copy is, not just where.
	if !strings.Contains(out, "nested worktree") {
		t.Fatalf("listing = %q, want the nested-worktree explanation", out)
	}
}

// A default that resolves cleanly still says which copy it picked — otherwise a
// launch from the wrong directory is indistinguishable from the right one.
func TestOfferRunChoice_DefaultEchoesResolvedPath(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	tasks := []allTask{
		{name: "App", dir: filepath.Join(root, "src", "App"), rel: "src/App", argv: []string{"dotnet", "run"}},
		{name: "Tool", dir: filepath.Join(root, "src", "Tool"), rel: "src/Tool", argv: []string{"dotnet", "run"}},
	}

	defer func(p bool) { dryRun = p }(dryRun)
	dryRun = true

	cmd, buf := newRunHost()
	if _, err := offerRunChoice(cmd, root, tasks, nil, "App", false); err != nil {
		t.Fatalf("offerRunChoice: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "defaultProject App → src/App") {
		t.Fatalf("output = %q, want the resolved default and its path echoed", got)
	}
}

// ---------------------------------------------------------------------------
// info's summary.
// ---------------------------------------------------------------------------

func TestProjectWarnings_LabelsDuplicatesAndTheDefault(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	all := []target{
		{Name: "Tweed.App", Dir: filepath.Join(root, filepath.FromSlash("ui/src/Tweed.App"))},
		{Name: "Tweed.App", Dir: filepath.Join(root, filepath.FromSlash("wt/x/ui/src/Tweed.App"))},
		{Name: "Tweed.Core", Dir: filepath.Join(root, filepath.FromSlash("ui/src/Tweed.Core"))},
	}
	cfg := config.Config{DefaultProject: "Tweed.App"}

	lines := projectWarnings(context.Background(), root, cfg, all, duplicateNames(all))
	if len(lines) != 1 {
		t.Fatalf("warnings = %v, want one duplicate summary", lines)
	}
	for _, want := range []string{"2 projects named Tweed.App", "(defaultProject)", "ui/src/Tweed.App", "wt/x/ui/src/Tweed.App"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("warning = %q, want it to mention %q", lines[0], want)
		}
	}
}
