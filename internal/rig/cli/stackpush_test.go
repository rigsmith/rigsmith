package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func TestStackPushPlanning(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	commit := func(content, message string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, dir, "add", "-A")
		mustGitStack(t, dir, "commit", "--allow-empty", "-qm", message)
		return strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
	}
	base := commit("base", "base")
	first := commit("first", "first")
	second := commit("second", "second\n\nFull message.")
	tree, _ := stackDeliveryTree(ctx, repo, second)
	squash, err := repo.CommitTree(ctx, tree, base, "squash")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("squash already sent", func(t *testing.T) {
		got, err := stackPushHead(ctx, repo, squash, second)
		if err != nil || got != squash {
			t.Fatalf("head=%s err=%v", got, err)
		}
	})
	third := commit("third", "third\n\nKeep the body.\n")
	fourth := commit("fourth", "fourth")
	for _, tc := range []struct {
		name, tip, head string
		want            int
	}{
		{"normal fast forward", base, fourth, 4},
		{"new after squash", squash, fourth, 2},
		{"partial squash", first, fourth, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stackPushHead(ctx, repo, tc.tip, tc.head)
			if err != nil {
				t.Fatal(err)
			}
			if err := stackRequireFastForward(ctx, repo, tc.tip, got, "app"); err != nil {
				t.Fatal(err)
			}
			entries, err := repo.LogRange(ctx, tc.tip, got)
			if err != nil || len(entries) != tc.want {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			actual, _ := stackDeliveryTree(ctx, repo, got)
			expected, _ := stackDeliveryTree(ctx, repo, tc.head)
			if actual != expected {
				t.Fatal("lost unsent content")
			}
			if tc.name == "normal fast forward" && got != fourth {
				t.Fatal("changed ordinary commit identity")
			}
			if tc.name == "new after squash" {
				old := mustGitStack(t, dir, "show", "-s", "--format=%an <%ae>%n%aI%n%cn <%ce>%n%cI%n%B", third)
				new := mustGitStack(t, dir, "show", "-s", "--format=%an <%ae>%n%aI%n%cn <%ce>%n%cI%n%B", got+"^")
				if old != new {
					t.Fatalf("metadata/message changed:\n%s\n%s", old, new)
				}
			}
		})
	}
	t.Run("partial squash keeps remaining commits", func(t *testing.T) {
		firstTree, _ := stackDeliveryTree(ctx, repo, first)
		partial, err := repo.CommitTree(ctx, firstTree, base, "partial squash")
		if err != nil {
			t.Fatal(err)
		}
		got, err := stackPushHead(ctx, repo, partial, second)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := repo.LogRange(ctx, partial, got)
		if len(entries) != 1 || entries[0].Subject != "second" {
			t.Fatalf("lost remaining change: %v", entries)
		}
	})
	t.Run("rebase merge counts as sent", func(t *testing.T) {
		baseTree, _ := stackDeliveryTree(ctx, repo, base)
		movedBase, err := repo.CommitTree(ctx, baseTree, base, "upstream empty commit")
		if err != nil {
			t.Fatal(err)
		}
		rebasedFirst, err := repo.ReparentCommit(ctx, first, movedBase)
		if err != nil {
			t.Fatal(err)
		}
		rebased, err := repo.ReparentCommit(ctx, second, rebasedFirst)
		if err != nil {
			t.Fatal(err)
		}
		if rebased == second {
			t.Fatal("fixture did not rebase")
		}
		got, err := stackPushHead(ctx, repo, rebased, second)
		if err != nil || got != rebased {
			t.Fatalf("head=%s err=%v", got, err)
		}
	})
	t.Run("merge commit remains fast forward", func(t *testing.T) {
		merge := strings.TrimSpace(mustGitStack(t, dir, "commit-tree", tree, "-p", squash, "-p", second, "-m", "merge"))
		got, err := stackPushHead(ctx, repo, squash, merge)
		if err != nil || got != squash {
			t.Fatalf("identical merge: %s %v", got, err)
		}
		mergedNew := strings.TrimSpace(mustGitStack(t, dir, "commit-tree", strings.TrimSpace(mustGitStack(t, dir, "rev-parse", fourth+"^{tree}")), "-p", squash, "-p", fourth, "-m", "merge with new work"))
		got, err = stackPushHead(ctx, repo, squash, mergedNew)
		if err != nil || got != mergedNew {
			t.Fatalf("merge changed: %s %v", got, err)
		}
	})
	t.Run("ambiguous merge replay refuses", func(t *testing.T) {
		newTree, _ := stackDeliveryTree(ctx, repo, fourth)
		merge := strings.TrimSpace(mustGitStack(t, dir, "commit-tree", newTree, "-p", second, "-p", third, "-m", "local merge"))
		if _, err := stackPushHead(ctx, repo, squash, merge); err == nil || !strings.Contains(err.Error(), "cannot safely replay merge") {
			t.Fatalf("expected refusal: %v", err)
		}
	})
	t.Run("overlap is ambiguous", func(t *testing.T) {
		overlap := commit("upstream overlap", "upstream")
		if _, err := stackPushHead(ctx, repo, overlap, second); err == nil || !strings.Contains(err.Error(), "no exact tree boundary") {
			t.Fatalf("expected refusal: %v", err)
		}
	})
	t.Run("client refuses stale history on permissive bare host", func(t *testing.T) {
		bare := filepath.Join(t.TempDir(), "app.git")
		mustGitStack(t, dir, "init", "--bare", bare)
		mustGitStack(t, bare, "config", "receive.denyNonFastForwards", "false")
		mustGitStack(t, dir, "push", bare, squash+":refs/heads/main")
		err := stackPushToUpstream(ctx, repo, bare, fourth, "main", "app")
		if err == nil || !strings.Contains(err.Error(), squash) || !strings.Contains(err.Error(), fourth) {
			t.Fatalf("expected both commits in refusal: %v", err)
		}
		if got := strings.TrimSpace(mustGitStack(t, bare, "rev-parse", "main")); got != squash {
			t.Fatal("remote moved")
		}
	})
}

func TestStackRestoreRefCancelledContext(t *testing.T) {
	ctx := context.Background()
	repo, err := gitrepo.Init(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Dir, "file"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "original"); err != nil {
		t.Fatal(err)
	}
	original, err := repo.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.RevParse(ctx, "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := repo.CommitTree(ctx, tree, original, "temporary filtered head")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, prior string }{
		{"delete temporary ref", ""},
		{"restore existing ref", original},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const ref = "refs/rigsmith/push/app"
			if err := repo.SetRef(ctx, ref, filtered); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := stackRestoreRef(canceled, repo, ref, tc.prior); err != nil {
				t.Fatalf("cleanup failed after command cancellation: %v", err)
			}
			if tc.prior == "" {
				if refExists(t, repo.Dir, ref) {
					t.Fatal("temporary ref survived cancellation")
				}
			} else if got, err := repo.RevParse(ctx, ref); err != nil || got != original {
				t.Fatalf("existing ref not restored: got %s, error %v", got, err)
			}
		})
	}
}
