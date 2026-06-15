package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
