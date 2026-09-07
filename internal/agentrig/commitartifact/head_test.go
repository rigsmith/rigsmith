package commitartifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSettledHeadReadsOnlyExactCommittedHistory(t *testing.T) {
	root := t.TempDir()
	if head, err := SettledHead(t.Context(), filepath.Join(root, "absent")); err != nil || head != "" {
		t.Fatalf("missing: %s %v", head, err)
	}
	repo := gitRepo{dir: root}
	mustRun(t, repo, "", "init", "--initial-branch=main")
	if head, err := SettledHead(t.Context(), root); err != nil || head != "" {
		t.Fatalf("unborn: %s %v", head, err)
	}
	repo.identity = []string{"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com"}
	putPublicationFile(t, root, "a", "committed")
	mustRun(t, repo, "", "add", "a")
	mustRun(t, repo, "", "commit", "-m", "fixture")
	want := mustRun(t, repo, "", "rev-parse", "HEAD")
	putPublicationFile(t, root, "a", "pending index")
	mustRun(t, repo, "", "add", "a")
	putPublicationFile(t, root, "a", "pending working tree")
	before := mustRun(t, repo, "", "status", "--porcelain")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	if head, err := SettledHead(t.Context(), root); err != nil || head != want {
		t.Fatalf("head: %s %v", head, err)
	}
	if got := mustRun(t, repo, "", "status", "--porcelain"); got != before {
		t.Fatal("changed index/worktree")
	}
	for _, state := range []string{"MERGE_HEAD", "MERGE_AUTOSTASH", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		p := filepath.Join(root, ".git", state)
		if err := os.WriteFile(p, []byte(want+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := SettledHead(t.Context(), root); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s: %v", state, err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SettledHead(t.Context(), root); err == nil {
		t.Fatal("invalid repository treated as unborn")
	}
}
