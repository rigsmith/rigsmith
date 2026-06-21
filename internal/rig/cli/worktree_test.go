package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/devroute"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func TestResolveWorktree(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-go-watch", Branch: "feat/go-watch"}
	gw2 := gitrepo.Worktree{Path: "/repo-wt/feat-go-watcher", Branch: "feat/go-watcher"}

	t.Run("no query, only main -> main", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main}, "")
		if err != nil || got != main.Path {
			t.Fatalf("got %q, %v; want %q", got, err, main.Path)
		}
	})

	t.Run("no query, one linked -> auto-select", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw}, "")
		if err != nil || got != gw.Path {
			t.Fatalf("got %q, %v; want %q", got, err, gw.Path)
		}
	})

	t.Run("exact branch query", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw, gw2}, "feat/go-watch")
		if err != nil || got != gw.Path {
			t.Fatalf("got %q, %v; want %q", got, err, gw.Path)
		}
	})

	t.Run("fuzzy query picks best match", func(t *testing.T) {
		got, err := resolveWorktree([]gitrepo.Worktree{main, gw, gw2}, "gowatch")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Both go-watch* match; the shorter branch wins the tie.
		if got != gw.Path {
			t.Fatalf("got %q; want %q", got, gw.Path)
		}
	})

	t.Run("no match errors", func(t *testing.T) {
		if _, err := resolveWorktree([]gitrepo.Worktree{main, gw}, "zzz"); err == nil {
			t.Fatal("expected error for non-matching query")
		}
	})
}

func TestWorktreeCompletions(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-go-watch", Branch: "feat/go-watch"}
	detached := gitrepo.Worktree{Path: "/repo-wt/detached", Branch: ""}

	t.Run("branch and short name, each described by path", func(t *testing.T) {
		got := worktreeCompletions([]gitrepo.Worktree{main, gw})
		want := []string{
			"main\t/repo",
			"feat/go-watch\t/repo-wt/feat-go-watch",
			"go-watch\t/repo-wt/feat-go-watch",
		}
		assertEqual(t, got, want)
	})

	t.Run("no short-name dup when it equals the branch", func(t *testing.T) {
		// "main" has no prefix to strip, so it must appear exactly once.
		got := worktreeCompletions([]gitrepo.Worktree{main})
		assertEqual(t, got, []string{"main\t/repo"})
	})

	t.Run("detached worktree contributes nothing", func(t *testing.T) {
		got := worktreeCompletions([]gitrepo.Worktree{detached})
		assertEqual(t, got, nil)
	})
}

func TestBranchAt(t *testing.T) {
	main := gitrepo.Worktree{Path: "/repo", Branch: "main"}
	gw := gitrepo.Worktree{Path: "/repo-wt/feat-x", Branch: "feat/x"}
	detached := gitrepo.Worktree{Path: "/repo-wt/d", Branch: ""}
	wts := []gitrepo.Worktree{main, gw, detached}

	if got := branchAt(wts, "/repo-wt/feat-x"); got != "feat/x" {
		t.Errorf("branchAt(known) = %q; want feat/x", got)
	}
	if got := branchAt(wts, "/repo-wt/d"); got != "(detached)" {
		t.Errorf("branchAt(detached) = %q; want (detached)", got)
	}
	if got := branchAt(wts, "/somewhere/else"); got != "else" {
		t.Errorf("branchAt(unknown) = %q; want base name 'else'", got)
	}
}

// TestWorktreeUseActiveUnset drives the three route subcommands end to end
// against a real repo + worktree, pinning under a temp HOME so the devroute file
// lands in the sandbox.
func TestWorktreeUseActiveUnset(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a", "1", "init")
	wtPath := filepath.Join(t.TempDir(), "feat")
	if err := r.WorktreeAdd(ctx, wtPath, "feature", "main", true); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := newWorktreeCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(ctx); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return buf.String()
	}

	if out := run("active", "--repo", r.Dir); !strings.Contains(out, "no pinned route") {
		t.Errorf("active before pinning = %q", out)
	}

	// No query + exactly one linked worktree → auto-selects it.
	if out := run("use", "--repo", r.Dir); !strings.Contains(out, "feature") {
		t.Errorf("use = %q", out)
	}
	pinned, _ := devroute.Read(r.Dir)
	if pinned == "" || !strings.Contains(pinned, "feat") {
		t.Fatalf("pinned route = %q; want the feature worktree", pinned)
	}

	if out := run("active", "--repo", r.Dir); !strings.Contains(out, "feature") || !strings.Contains(out, pinned) {
		t.Errorf("active after pinning = %q", out)
	}

	if out := run("unset", "--repo", r.Dir); !strings.Contains(out, "cleared") {
		t.Errorf("unset = %q", out)
	}
	if got, _ := devroute.Read(r.Dir); got != "" {
		t.Errorf("after unset pinned = %q; want empty", got)
	}
}

// The rig menu's Worktrees group carries the worktree lifecycle commands and no
// longer surfaces the retired -dev route pinning.
func TestWorktreeMenuItems(t *testing.T) {
	items := worktreeMenuItems()
	if len(items) == 0 {
		t.Fatal("expected worktree menu items")
	}
	labels := map[string]bool{}
	for _, it := range items {
		if it.cmd == nil {
			t.Errorf("worktree item %q should carry a prebuilt command", it.label)
		}
		labels[it.label] = true
		if strings.Contains(it.desc, "-dev") || it.label == "route" {
			t.Errorf("worktree menu must not surface the -dev route anymore, got %q", it.label)
		}
	}
	for _, want := range []string{"new", "list", "open", "rm"} {
		if !labels[want] {
			t.Errorf("worktree menu missing lifecycle item %q, got %+v", want, items)
		}
	}
}

// Bare `rig wt` on a TTY drives an explicit climenu over these entries (not
// climenu.Run's subcommand introspection, which would offer only `list`), so
// new/open/rm must come through — each carrying its prompting wrapper command.
func TestWorktreeMenuEntries(t *testing.T) {
	entries := worktreeMenuEntries()
	labels := map[string]bool{}
	for _, e := range entries {
		if e.Cmd == nil {
			t.Errorf("worktree entry %q should carry a command", e.Label)
		}
		if strings.Contains(e.Desc, "-dev") {
			t.Errorf("worktree entry %q must not mention the -dev route, got %q", e.Label, e.Desc)
		}
		labels[e.Label] = true
	}
	for _, want := range []string{"new", "list", "open", "rm"} {
		if !labels[want] {
			t.Errorf("bare `rig wt` menu missing lifecycle entry %q, got %+v", want, entries)
		}
	}
}

// The worktree group's help (the climenu header is its Short) no longer
// advertises the retired -dev route or the moved-out `wt prune`.
func TestWorktreeCmdHelpHasNoDevRoute(t *testing.T) {
	cmd := newWorktreeCmd()
	for _, text := range []string{cmd.Short, cmd.Long} {
		if strings.Contains(text, "-dev") {
			t.Errorf("worktree help must not mention the -dev route:\n%s", text)
		}
		if strings.Contains(text, "wt prune") || strings.Contains(text, "list | prune") {
			t.Errorf("worktree help points at the moved-out `wt prune` (now `rig prune`):\n%s", text)
		}
	}
}

func assertEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d got %q; want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
