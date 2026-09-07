package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

func TestE2E_SharedMemoryLinkRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("gated: CLAUDERIG_E2E=1")
	}
	fixtureGit(t)
	ctx := t.Context()
	srcHome, dstHome := t.TempDir(), t.TempDir()
	source, target := filepath.Join(srcHome, ".claude"), filepath.Join(dstHome, ".claude")
	srcMain := project.Flatten(filepath.Join(srcHome, "Git", "main"))
	srcWork := project.Flatten(filepath.Join(srcHome, "Git", "worktree"))
	for _, name := range []string{"main", "worktree"} {
		cwd := filepath.Join(srcHome, "Git", name)
		write(t, source, "projects/"+project.Flatten(cwd)+"/s.jsonl", `{"type":"user","cwd":"`+jsonEsc(cwd)+`"}`+"\n")
	}
	write(t, source, "projects/"+srcMain+"/memory/MEMORY.md", "shared facts")
	if err := os.Symlink(filepath.Join(source, "projects", srcMain, "memory"), filepath.Join(source, "projects", srcWork, "memory")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	stage := t.TempDir()
	_, err := engine.Sync(engine.Options{StagingDir: stage, Config: cliOnly(source),
		Machine: config.Machine{Name: "source", OS: config.OSToken(), Home: srcHome}})
	must(t, err)
	bare := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
	repo, err := gitrepo.Init(ctx, stage)
	must(t, err)
	must(t, repo.SetRemote(ctx, "origin", bare))
	changed, err := repo.Commit(ctx, "shared memory fixture")
	must(t, err)
	if !changed {
		t.Fatal("fixture did not commit")
	}
	must(t, repo.Push(ctx, "origin", "main"))
	clone := filepath.Join(t.TempDir(), "clone")
	_, err = gitrepo.Clone(ctx, bare, clone)
	must(t, err)
	m, err := manifest.Load(clone)
	must(t, err)
	if m.Links["projects/"+srcWork+"/memory"] != "projects/"+srcMain+"/memory" {
		t.Fatal("link discovery did not survive Git transport", m.Links)
	}
	report, err := engine.Restore(engine.RestoreOptions{StagingDir: clone, Config: cliOnly(target), Manifest: m,
		Machine: config.Machine{Name: "target", OS: config.OSToken(), Home: dstHome}})
	must(t, err)
	if len(report.Roots) != 1 || report.Roots[0].Links != 1 {
		t.Fatal("link did not restore", report)
	}
	dstMain := project.Flatten(filepath.Join(dstHome, "Git", "main"))
	dstWork := project.Flatten(filepath.Join(dstHome, "Git", "worktree"))
	link := filepath.Join(target, "projects", dstWork, "memory")
	got, err := os.Readlink(link)
	must(t, err)
	if got != filepath.Join("..", dstMain, "memory") {
		t.Fatalf("wrong rewritten relative target: %q", got)
	}
	if got := read(t, filepath.Join(link, "MEMORY.md")); got != "shared facts" {
		t.Fatal("shared memory lost", got)
	}
}
