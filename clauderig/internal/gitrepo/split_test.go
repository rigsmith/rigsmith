package gitrepo

import (
	"context"
	"strings"
	"testing"
)

// CommitSubtree must build a config-history branch containing everything except
// the excluded tree, independent of main, and survive a squash of main.
func TestCommitSubtree_ConfigHistorySurvivesSquash(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	write(t, r.Dir, "cli/settings.json", "{}")
	write(t, r.Dir, "cli/projects/-p/s.jsonl", "transcript")
	if _, err := r.Commit(ctx, "sync 1"); err != nil {
		t.Fatal(err)
	}

	// mirror config-only to config-history
	changed, err := r.CommitSubtree(ctx, "config-history", []string{".", ":!cli/projects"}, "config 1")
	if err != nil || !changed {
		t.Fatalf("CommitSubtree: changed=%v err=%v", changed, err)
	}

	// config-history contains settings.json but NOT the transcript
	files, _ := runGit(ctx, r.Dir, "ls-tree", "-r", "--name-only", "config-history")
	if !strings.Contains(files, "cli/settings.json") {
		t.Errorf("config-history missing settings.json:\n%s", files)
	}
	if strings.Contains(files, "cli/projects") {
		t.Errorf("config-history should NOT contain projects:\n%s", files)
	}

	// a second config commit extends the branch (parented)
	write(t, r.Dir, "cli/settings.json", `{"v":2}`)
	if changed, err := r.CommitSubtree(ctx, "config-history", []string{".", ":!cli/projects"}, "config 2"); err != nil || !changed {
		t.Fatalf("second CommitSubtree: changed=%v err=%v", changed, err)
	}
	count, _ := runGit(ctx, r.Dir, "rev-list", "--count", "config-history")
	if strings.TrimSpace(count) != "2" {
		t.Errorf("config-history commits = %s, want 2", strings.TrimSpace(count))
	}

	// squashing main does not touch config-history
	if err := r.Squash(ctx, "squash"); err != nil {
		t.Fatal(err)
	}
	count2, _ := runGit(ctx, r.Dir, "rev-list", "--count", "config-history")
	if strings.TrimSpace(count2) != "2" {
		t.Errorf("config-history should be untouched by squash, commits = %s", strings.TrimSpace(count2))
	}

	// no-op when nothing changed
	if changed, _ := r.CommitSubtree(ctx, "config-history", []string{".", ":!cli/projects"}, "config 3"); changed {
		t.Error("expected no-op CommitSubtree when config unchanged")
	}
}
