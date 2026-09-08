package service_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

func TestStageUnresolvedMergeWithClaudePolicy(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic merge staging")
	}
	for _, kind := range []string{"append", "edited-worktree", "secret"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			git(t, root, "init", "--initial-branch=main")
			if err := backupgit.Ensure(root); err != nil {
				t.Fatal(err)
			}
			const path = "cli/projects/-p/append.jsonl"
			const base = "{\"uuid\":\"base\"}\n"
			const ours = "{\"uuid\":\"local\"}\n"
			theirs := "{\"uuid\":\"incoming\"}\n"
			if kind == "secret" {
				theirs = "{\"uuid\":\"incoming\",\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
			}
			put(t, root, path, base)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "base")
			git(t, root, "checkout", "-b", "incoming")
			put(t, root, path, base+theirs)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "incoming")
			git(t, root, "checkout", "main")
			put(t, root, path, base+ours)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "local")
			original := git(t, root, "rev-parse", "HEAD")
			if _, err := remoteGit(t.Context(), root, "merge", "--no-commit", "incoming"); err == nil {
				t.Fatal("expected unresolved merge")
			}
			if kind == "edited-worktree" {
				put(t, root, path, "later manual resolution\n")
			}
			before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			live, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			policy := commitartifact.MergeStagePolicy{PolicyID: "claude-fixture-v1", MergePlanPolicy: commitartifact.MergePlanPolicy{
				MergeFinishPolicy: commitartifact.MergeFinishPolicy{Message: "recover backup merge", AuthorName: "fixture", AuthorEmail: "fixture@example.com", Time: time.Unix(100, 0), Validate: backupgit.ValidateTree, Audit: engine.CheckPublishContext}, Resolve: mergepolicy.ResolveRetainedFiles}}
			store := commitartifact.MergeStageStore{Dir: filepath.Join(t.TempDir(), "intent")}
			tree, err := store.Stage(t.Context(), root, policy)
			if git(t, root, "rev-parse", "HEAD") != original {
				t.Fatal("staging moved HEAD")
			}
			if kind != "append" {
				want := commitartifact.ErrConflict
				if kind == "secret" {
					want = engine.ErrSecretTripwire
				}
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
				got, readErr := os.ReadFile(filepath.Join(root, ".git", "index"))
				if readErr != nil || !bytes.Equal(got, before) {
					t.Fatal("changed rejected index", readErr)
				}
				got, readErr = os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if readErr != nil || !bytes.Equal(got, live) {
					t.Fatal("changed rejected bytes", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			show := exec.CommandContext(t.Context(), "git", "show", ":"+path)
			show.Dir = root
			staged, err := show.Output()
			if err != nil {
				t.Fatal(err)
			}
			merged, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			want := []byte(base + ours + theirs)
			if !bytes.Equal(staged, want) || !bytes.Equal(merged, want) {
				t.Fatalf("lost append bytes: staged=%q worktree=%q", staged, merged)
			}
			if got := git(t, root, "write-tree"); got != tree {
				t.Fatal("wrong staged tree")
			}
			if _, err := commitartifact.FinishStagedMerge(t.Context(), root, policy.MergeFinishPolicy); err != nil {
				t.Fatal(err)
			}
		})
	}
}
