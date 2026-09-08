package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
)

// The planner consumes Claude's existing policy without enabling canonical repair.
func TestPlanUnresolvedMergeWithClaudePolicy(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic unresolved merge planning")
	}
	for _, kind := range []string{"append", "edited", "secret"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			git(t, root, "init", "--initial-branch=main")
			if err := backupgit.Ensure(root); err != nil {
				t.Fatal(err)
			}
			const path = "cli/projects/-p/append.jsonl"
			const base = "{\"uuid\":\"base\"}\n"
			const ours = "{\"uuid\":\"local\"}\n"
			const theirs = "{\"uuid\":\"incoming\"}\n"
			put(t, root, path, base)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "base")
			git(t, root, "checkout", "-b", "incoming")
			incomingBytes := base + theirs
			if kind == "edited" {
				incomingBytes = "{\"uuid\":\"base\",\"changed\":true}\n" + theirs
			}
			if kind == "secret" {
				incomingBytes = base + "{\"uuid\":\"incoming\",\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
			}
			put(t, root, path, incomingBytes)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "incoming append")
			git(t, root, "checkout", "main")
			put(t, root, path, base+ours)
			git(t, root, "add", ".")
			git(t, root, "commit", "-m", "local append")
			original := git(t, root, "rev-parse", "HEAD")
			if _, err := remoteGit(t.Context(), root, "merge", "--no-commit", "incoming"); err == nil {
				t.Fatal("expected unresolved append")
			}
			before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "plan")
			plan, err := commitartifact.PlanUnresolvedMerge(t.Context(), root, dest, commitartifact.MergePlanPolicy{
				MergeFinishPolicy: commitartifact.MergeFinishPolicy{Message: "planned backup merge", AuthorName: "fixture", AuthorEmail: "fixture@example.com", Time: time.Unix(100, 0), Validate: backupgit.ValidateTree, Audit: engine.CheckPublishContext},
				Resolve:           mergepolicy.ResolveRetainedFiles,
			})
			if got, readErr := os.ReadFile(filepath.Join(root, ".git", "index")); readErr != nil || string(got) != string(before) || git(t, root, "rev-parse", "HEAD") != original {
				t.Fatal("planner changed canonical merge", readErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); statErr != nil {
				t.Fatal("planner removed pending merge", statErr)
			}
			if kind != "append" {
				want := commitartifact.ErrConflict
				if kind == "secret" {
					want = engine.ErrSecretTripwire
				}
				if !errors.Is(err, want) || plan != (commitartifact.MergePlan{}) {
					t.Fatalf("accepted unsafe append: %+v %v", plan, err)
				}
				if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
					t.Fatal("retained unsafe plan", statErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			inspect := filepath.Join(t.TempDir(), "inspect.git")
			git(t, filepath.Dir(inspect), "init", "--bare", inspect)
			git(t, inspect, "fetch", plan.BundlePath, "refs/rig/merge-plan:refs/heads/plan")
			if got := git(t, inspect, "show", plan.Commit+":"+path); got != strings.TrimSpace(base+ours+theirs) {
				t.Fatalf("lost native append bytes: %q", got)
			}
		})
	}
}
