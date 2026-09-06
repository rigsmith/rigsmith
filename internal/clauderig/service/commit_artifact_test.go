package service_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func capturedCommitFixture(t *testing.T, seeded bool) service.ArtifactCommitRequest {
	t.Helper()
	req := artifactCaptureFixture(t, "sealed queued bytes")
	if seeded {
		svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
		if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
			t.Fatal(err)
		}
	}
	ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Work.CaptureRef, req.Work.Phase = ref, queue.Captured
	return service.ArtifactCommitRequest{Capture: req, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
}

func TestCommitArtifactRetainsSnapshotAndAncestryWithoutChangingStaging(t *testing.T) {
	req := capturedCommitFixture(t, true)
	stage := req.Capture.Sync.StagingDir
	seed := git(t, stage, "rev-parse", "HEAD")
	// Both canonical HEAD and its index can advance after capture. Neither is
	// an input for the queued snapshot or permission to overwrite newer work.
	put(t, stage, "newer", "newer canonical commit")
	git(t, stage, "add", "newer")
	git(t, stage, "commit", "-m", "newer synchronous work")
	head := git(t, stage, "rev-parse", "HEAD")
	put(t, stage, "pending", "pending canonical index")
	git(t, stage, "add", "pending")
	status := git(t, stage, "status", "--porcelain")
	if err := os.RemoveAll(filepath.Join(req.Capture.Sync.Machine.Home, ".claude")); err != nil {
		t.Fatal(err)
	}
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { t.Fatal("read worker identity"); return service.Identity{}, nil }}
	ref, err := svc.CommitArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	info, err := commitartifact.Open(t.Context(), req.Commits, ref, filepath.Join(t.TempDir(), "commit"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Parent != seed || info.CaptureRef != req.Capture.Work.CaptureRef {
		t.Fatalf("wrong retained ancestry: %+v", info)
	}
	clone := filepath.Join(t.TempDir(), "clone.git")
	git(t, filepath.Dir(clone), "init", "--bare", clone)
	git(t, clone, "fetch", info.BundlePath, commitartifact.RefName+":refs/heads/main")
	data := git(t, clone, "show", info.Commit+":cli/projects/-workspace-acme/s.jsonl")
	if !strings.Contains(data, "sealed queued bytes") {
		t.Fatal("lost captured data")
	}
	if git(t, clone, "rev-parse", info.Commit+"^") != seed {
		t.Fatal("wrong parent")
	}
	if got := git(t, clone, "ls-tree", "--name-only", info.Commit); strings.Contains(got, "newer") || strings.Contains(got, "pending") {
		t.Fatal("swept canonical changes into commit", got)
	}
	if git(t, stage, "rev-parse", "HEAD") != head || git(t, stage, "status", "--porcelain") != status {
		t.Fatal("changed canonical checkout")
	}
	// The bundle must remain independent of both original repositories, including
	// after GC would have been able to remove the capture's original seed.
	if err := os.RemoveAll(stage); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(req.Capture.Store.Dir); err != nil {
		t.Fatal(err)
	}
	again, err := svc.CommitArtifact(t.Context(), req)
	if err != nil || again != ref {
		t.Fatalf("retry needed original inputs: %s %v", again, err)
	}
	if _, err := commitartifact.Open(t.Context(), req.Commits, ref, filepath.Join(t.TempDir(), "retry")); err != nil {
		t.Fatal(err)
	}
}

func TestCommitArtifactRejectsInvalidBatchAndMissingSeed(t *testing.T) {
	for _, mode := range []string{"binding", "provenance", "phase", "capture-key", "missing-capture", "missing-seed", "overlap"} {
		t.Run(mode, func(t *testing.T) {
			req := capturedCommitFixture(t, mode == "missing-seed")
			switch mode {
			case "binding":
				req.Capture.Sync.Config.Remote = "changed"
			case "provenance":
				req.Capture.Identity = service.Identity{}
			case "phase":
				req.Capture.Work.Phase = queue.Committed
			case "capture-key":
				req.Capture.Work.CaptureRef = artifact.Key([]byte("other batch")) + ":" + strings.Repeat("0", 64)
			case "missing-capture":
				if err := os.RemoveAll(req.Capture.Store.Dir); err != nil {
					t.Fatal(err)
				}
			case "missing-seed":
				if err := os.RemoveAll(commitartifact.SeedStore(req.Capture.Store).Dir); err != nil {
					t.Fatal(err)
				}
			case "overlap":
				req.Commits.Dir = req.Capture.Store.Dir
			}
			ref, err := (service.Service{}).CommitArtifact(t.Context(), req)
			if err == nil || ref != "" {
				t.Fatalf("invalid commit succeeded: %s %v", ref, err)
			}
			if (mode == "binding" || mode == "provenance" || mode == "capture-key") && !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
			if mode != "overlap" {
				if paths, _ := filepath.Glob(filepath.Join(req.Commits.Dir, "*.capture")); len(paths) != 0 {
					t.Fatal("failed commit sealed", paths)
				}
			}
		})
	}
}

func TestCommitArtifactAutoModeRetryWithoutStorageMarker(t *testing.T) {
	req := artifactCaptureFixture(t, "auto-mode sealed bytes")
	req.Sync.Config.ChunkTranscripts = nil
	put(t, req.Sync.StagingDir, "clauderig-storage.json", `{"version":1,"chunkedTranscripts":true}`)
	var err error
	req.Binding, err = service.CaptureBinding(req.Sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Work.CaptureRef, req.Work.Phase = ref, queue.Captured
	input := service.ArtifactCommitRequest{Capture: req, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
	commitRef, err := (service.Service{}).CommitArtifact(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(req.Sync.StagingDir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(req.Store.Dir); err != nil {
		t.Fatal(err)
	}
	again, err := (service.Service{}).CommitArtifact(t.Context(), input)
	if err != nil || again != commitRef {
		t.Fatalf("auto retry depended on deleted staging: %s %v", again, err)
	}
	// Reusing the sealed mode must not bypass other live binding checks.
	input.Capture.Sync.Config.Remote = "different destination"
	if _, err := (service.Service{}).CommitArtifact(t.Context(), input); !errors.Is(err, queue.ErrBinding) {
		t.Fatalf("changed config accepted: %v", err)
	}
}

func TestFirstCommitUsesRetainedSeedAfterCanonicalHistoryDisappears(t *testing.T) {
	for _, mode := range []string{"repository-deleted", "seed-pruned"} {
		t.Run(mode, func(t *testing.T) {
			req := capturedCommitFixture(t, true)
			stage := req.Capture.Sync.StagingDir
			seed := git(t, stage, "rev-parse", "HEAD")
			meta, err := req.Capture.Store.Metadata(t.Context(), req.Capture.Work.CaptureRef)
			if err != nil || meta.BaseReference != seed || meta.SeedReference == "" {
				t.Fatalf("capture did not retain seed: %+v %v", meta, err)
			}
			if err := os.RemoveAll(filepath.Join(req.Capture.Sync.Machine.Home, ".claude")); err != nil {
				t.Fatal(err)
			}
			if mode == "repository-deleted" {
				if err := os.RemoveAll(stage); err != nil {
					t.Fatal(err)
				}
			} else {
				git(t, stage, "checkout", "--orphan", "replacement")
				git(t, stage, "rm", "-rf", ".")
				put(t, stage, "replacement.txt", "unrelated replacement history")
				git(t, stage, "add", "replacement.txt")
				git(t, stage, "commit", "-m", "replacement root")
				git(t, stage, "branch", "-D", "main")
				git(t, stage, "reflog", "expire", "--expire=now", "--all")
				git(t, stage, "gc", "--prune=now")
				probe := exec.Command("git", "cat-file", "-e", seed+"^{commit}")
				probe.Dir = stage
				if err := probe.Run(); err == nil {
					t.Fatal("fixture did not prune original seed")
				}
			}
			ref, err := (service.Service{}).CommitArtifact(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			info, err := commitartifact.Open(t.Context(), req.Commits, ref, filepath.Join(t.TempDir(), "opened"))
			if err != nil || info.Parent != seed {
				t.Fatalf("lost retained parent: %+v %v", info, err)
			}
			clone := filepath.Join(t.TempDir(), "clone.git")
			git(t, filepath.Dir(clone), "init", "--bare", clone)
			git(t, clone, "fetch", info.BundlePath, commitartifact.RefName+":refs/heads/main")
			if got := git(t, clone, "show", info.Commit+":cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed queued bytes") {
				t.Fatal("lost captured bytes")
			}
		})
	}
}
