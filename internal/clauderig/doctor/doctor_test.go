package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func TestCheckGuide_FixInstalls(t *testing.T) {
	dir := t.TempDir()
	env := Env{RepoRoot: dir, ClaudeMd: filepath.Join(dir, "CLAUDE.md")}

	r := checkGuide(env)
	if r.Status != Warn || r.Fix == nil {
		t.Fatalf("absent guide: got %+v, want Warn with Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkGuide(env); r2.Status != OK {
		t.Fatalf("after fix: %+v, want OK", r2)
	}
}

func TestCheckGlobalHooks_FixInstalls(t *testing.T) {
	env := Env{UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	r := checkGlobalHooks(env)
	if r.Status != Warn || r.Fix == nil {
		t.Fatalf("no hooks: got %+v, want Warn with Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkGlobalHooks(env); r2.Status != OK {
		t.Fatalf("after fix: %+v, want OK", r2)
	}
}

func TestCheckLocalGitignore_SkippedWhenNoLocalFile(t *testing.T) {
	env := Env{RepoRoot: t.TempDir(), LocalSettings: filepath.Join(t.TempDir(), "settings.local.json")}
	if _, ok := checkLocalGitignore(env); ok {
		t.Error("expected the local-gitignore check to be skipped when no local settings file exists")
	}
}

func TestRun_NoRepoSkipsRepoChecks(t *testing.T) {
	env := Env{
		UserSettings: filepath.Join(t.TempDir(), "settings.json"),
		// RepoRoot empty ⇒ not in a repo
	}
	sections := Run(context.Background(), env)
	var wt *Section
	for i := range sections {
		if sections[i].Title == "worktree discipline" {
			wt = &sections[i]
		}
	}
	if wt == nil {
		t.Fatal("no worktree section")
	}
	for _, r := range wt.Results {
		if r.Name == "guard hook" {
			t.Error("guard check should be skipped outside a repo")
		}
	}
	_ = os.Stdout
}

// The pair that misled for ten days: a fresh local commit (so `last sync` is
// green) sitting on top of a push that has been rejected every time. `pushed`
// exists to fail in exactly that state.
func TestCheckPushed_FailsWhenNothingReachedTheRemote(t *testing.T) {
	ctx := context.Background()
	bare := t.TempDir()
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Skip("git unavailable")
	}
	staging := t.TempDir()
	repo, err := gitrepo.Init(ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRemote(ctx, "origin", bare); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	// A second commit that never gets pushed — the ten-day state.
	if err := os.WriteFile(filepath.Join(staging, "b.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "never pushed"); err != nil {
		t.Fatal(err)
	}

	env := Env{Cfg: &config.Config{}, Staging: staging, UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	if got := checkPushed(ctx, env); got.Status != Fail {
		t.Fatalf("pushed = %v (%q), want Fail — an unpushed backup is broken, not untidy", got.Status, got.Detail)
	}
	// And it passes once the commit actually lands.
	if err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if got := checkPushed(ctx, env); got.Status != OK {
		t.Fatalf("after pushing: pushed = %v (%q), want OK", got.Status, got.Detail)
	}
}

// The gap Copilot found in the first cut: a remote is configured, commits are
// piling up locally, and nothing has ever reached it — so there is no
// remote-tracking ref and ahead/behind are both 0. Reporting "up to date with
// origin/main" there is the same false green this check exists to remove.
func TestCheckPushed_NeverPushedToAConfiguredRemote(t *testing.T) {
	ctx := context.Background()
	staging := t.TempDir()
	repo, err := gitrepo.Init(ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRemote(ctx, "origin", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "local only"); err != nil {
		t.Fatal(err)
	}

	env := Env{Cfg: &config.Config{Remote: "https://example.invalid/x.git"}, Staging: staging,
		UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	got := checkPushed(ctx, env)
	if got.Status == OK {
		t.Fatalf("pushed = OK (%q) — nothing has ever reached the remote", got.Detail)
	}
}
