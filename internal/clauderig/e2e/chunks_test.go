package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/peek"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestE2E_ChunkedRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") == "" {
		t.Skip("gated: CLAUDERIG_E2E=1")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := t.Context()
	live, stage := t.TempDir(), t.TempDir()
	const rel = "cli/projects/-p/aaaaaaaa-0000-0000-0000-000000000000.jsonl"
	body := `{"type":"user","cwd":"/p","message":{"role":"user","content":"chunked opening prompt"}}` + "\n" + strings.Repeat(`{"type":"assistant","message":{"content":"ordinary filler"}}`+"\n", 150000)
	write(t, live, strings.TrimPrefix(rel, "cli/"), body)
	opts := engine.Options{StagingDir: stage, Config: cliOnly(live), Machine: config.Machine{Name: "src", OS: pathmap.OSMacOS, Home: t.TempDir()}, ChunkTranscripts: true}
	if _, err := engine.Sync(opts); err != nil {
		t.Fatal(err)
	}
	repo, err := gitrepo.Init(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "clauderig sync: src"); err != nil {
		t.Fatal(err)
	}
	tail := `{"type":"user","message":{"role":"user","content":"unique appended question"}}` + "\n"
	write(t, live, strings.TrimPrefix(rel, "cli/"), body+tail)
	if _, err := engine.Sync(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "clauderig sync: src"); err != nil {
		t.Fatal(err)
	}
	// History resolves chunk references at the requested revision, even after the
	// working tree removes its old tail. An append changes only index + tail.
	for _, tc := range []struct{ ref, want string }{{"HEAD^", body}, {"HEAD", body + tail}} {
		got, err := peek.Read(ctx, repo, tc.ref, peek.Session{Path: rel})
		if err != nil || string(got) != tc.want {
			t.Fatalf("peek %s: %v", tc.ref, err)
		}
	}
	diff := exec.Command("git", "diff", "--no-renames", "--name-status", "HEAD^", "HEAD", "--", rel+".chunks")
	diff.Dir = stage
	out, err := diff.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(out)), "\n")) != 2 {
		t.Fatalf("append should replace only tail: %s", out)
	}
	bare := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
	must(t, repo.SetRemote(ctx, "origin", bare))
	must(t, engine.CheckPublish(stage))
	must(t, repo.Push(ctx, "origin", "main"))
	cloned := filepath.Join(t.TempDir(), "repo")
	if _, err := gitrepo.Clone(ctx, bare, cloned); err != nil {
		t.Fatal(err)
	}
	mode, err := transcript.Enabled(cloned)
	if err != nil || !mode {
		t.Fatalf("clone did not inherit chunking: %v", err)
	}
	p := filepath.Join(cloned, filepath.FromSlash(rel))
	if title := session.FirstPrompt(p); title != "chunked opening prompt" {
		t.Fatalf("title: %s", title)
	}
	n, err := search.ScanFile(p, search.Options{Query: "unique appended question"}, func(search.Match) {})
	if err != nil || n != 1 {
		t.Fatalf("search: %d %v", n, err)
	}
	target := t.TempDir()
	if _, err := engine.Restore(engine.RestoreOptions{StagingDir: cloned, Config: cliOnly(target), Machine: opts.Machine, TargetOverride: map[string]string{"cli": target}}); err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(target, strings.TrimPrefix(rel, "cli/")))
	if err != nil || !bytes.Equal(native, []byte(body+tail)) {
		t.Fatalf("clone restore: %v", err)
	}
	if err := transcript.ConvertTree(cloned, false); err != nil {
		t.Fatal(err)
	}
	native, err = os.ReadFile(p)
	if err != nil || string(native) != body+tail {
		t.Fatalf("rollback: %v", err)
	}
}
