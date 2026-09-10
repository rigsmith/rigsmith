package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

var reclamationBinding = queue.Binding{Vendor: "fixture", StoreID: "stage", RootID: "roots", RemoteID: "remote", ConfigID: "policy"}

func reclamationFixture(t *testing.T) (WorkspaceCleanup, *queue.Queue, []string) {
	t.Helper()
	req := cleanupFixture(t)
	q, err := queue.Create(t.Context(), filepath.Join(t.TempDir(), "queue"), reclamationBinding)
	if err != nil {
		t.Fatal(err)
	}
	// Model a completed queue lifecycle; a brand-new empty queue must not reclaim.
	if _, err := q.Enqueue(t.Context(), queue.Request{EventID: "baseline", SessionID: "s", ProvenanceID: "p", Flush: queue.Flush{Mode: queue.Normal}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	worker, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	work, err := worker.Next(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		phase queue.Phase
		ref   string
	}{{queue.Captured, "capture"}, {queue.Committed, "commit"}, {queue.Pushed, ""}} {
		if err := worker.Progress(t.Context(), work.ID, step.phase, step.ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := worker.Acknowledge(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	worker.Close()
	var files []string
	for i, store := range []artifact.Store{req.Captures, SeedStore(req.Captures), req.Commits} {
		key := artifact.Key([]byte{byte(i)})
		if _, err := store.Build(t.Context(), key, func(_ context.Context, tree string) error {
			return os.WriteFile(filepath.Join(tree, "fixture"), []byte("sealed"), 0600)
		}); err != nil {
			t.Fatal(err)
		}
		files = append(files, filepath.Join(store.Dir, key+".capture"))
	}
	return req, q, files
}
func reclaim(t *testing.T, req WorkspaceCleanup, q *queue.Queue) (QueueReclamationResult, error) {
	t.Helper()
	var result QueueReclamationResult
	err := q.Maintain(t.Context(), reclamationBinding, func(m *queue.Maintenance) error {
		var err error
		result, err = ReclaimQueueArtifacts(t.Context(), req, m)
		return err
	})
	return result, err
}

func TestReclaimQueueArtifactsProtectsEveryUnfinishedPhase(t *testing.T) {
	for _, mode := range []string{"pending", "running", "captured", "committed", "pushed", "blocked", "done"} {
		t.Run(mode, func(t *testing.T) {
			req, q, files := reclamationFixture(t)
			request := queue.Request{EventID: "event", SessionID: "s", ProvenanceID: "p", Flush: queue.Flush{Mode: queue.Normal}}
			if _, err := q.Enqueue(t.Context(), request, time.Now()); err != nil {
				t.Fatal(err)
			}
			if mode != "pending" {
				w, err := q.Worker(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer w.Close()
				work, err := w.Next(t.Context(), time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if mode != "running" && mode != "blocked" {
					if err := w.Progress(t.Context(), work.ID, queue.Captured, "capture"); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "committed" || mode == "pushed" || mode == "done" {
					if err := w.Progress(t.Context(), work.ID, queue.Committed, "commit"); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "pushed" || mode == "done" {
					if err := w.Progress(t.Context(), work.ID, queue.Pushed, ""); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "done" {
					if err := w.Acknowledge(t.Context(), work.ID); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "blocked" {
					if err := w.Retry(t.Context(), work.ID, time.Now(), "repair", true); err != nil {
						t.Fatal(err)
					}
				}
				w.Close()
			}
			before, err := os.ReadFile(filepath.Join(q.Directory(), "queue.json"))
			if err != nil {
				t.Fatal(err)
			}
			cleanupPut(t, q.Directory(), ".confirmation-dead/git/config")
			cleanupPut(t, req.Captures.Dir, ".capture-work-dead/file")
			result, err := reclaim(t, req, q)
			if err != nil || result.RemovedWorkspaces != 2 {
				t.Fatal(result, err)
			}
			if mode == "done" {
				if result.RemovedArchives != 3 || result.ProtectionReason != "" {
					t.Fatal(result)
				}
			} else if result.RemovedArchives != 0 || result.ProtectionReason != "pending-work" {
				t.Fatal(result)
			}
			for _, path := range files {
				_, err := os.Stat(path)
				if mode == "done" {
					if !os.IsNotExist(err) {
						t.Fatal(path, err)
					}
				} else if err != nil {
					t.Fatal("lost saved output", path, err)
				}
			}
			after, err := os.ReadFile(filepath.Join(q.Directory(), "queue.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("changed queue state", err)
			}
		})
	}
}

func TestReclaimQueueArtifactsRetainsRecoveryAndUnknownState(t *testing.T) {
	for _, mode := range []string{"recovery", "unknown", "corrupt", "linked-archive", "queue-candidate"} {
		t.Run(mode, func(t *testing.T) {
			req, q, files := reclamationFixture(t)
			scratch := cleanupPut(t, req.Captures.Dir, ".capture-work-dead/file")
			switch mode {
			case "recovery":
				cleanupPut(t, req.Captures.Dir, "merge-recovery-key/intent.capture")
			case "unknown":
				cleanupPut(t, SeedStore(req.Captures).Dir, "unknown")
			case "corrupt":
				if err := os.WriteFile(files[2], []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "linked-archive":
				if err := os.Remove(files[2]); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(files[0], files[2]); err != nil {
					t.Skip(err)
				}
			case "queue-candidate":
				cleanupPut(t, q.Directory(), ".confirmation-file")
			}
			result, err := reclaim(t, req, q)
			if mode == "recovery" || mode == "unknown" {
				if err != nil || result.ProtectionReason != "retained-state" || result.RemovedWorkspaces != 1 {
					t.Fatal(result, err)
				}
			} else {
				if err == nil || result.RemovedWorkspaces != 0 {
					t.Fatal(result, err)
				}
				if _, err := os.Stat(scratch); err != nil {
					t.Fatal("deleted before complete validation", err)
				}
			}
			if result.RemovedArchives != 0 {
				t.Fatal(result)
			}
			for _, path := range files {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("deleted retained archive", err)
				}
			}
		})
	}
}

func TestReclaimQueueArtifactsOwnershipAndPartialRetry(t *testing.T) {
	req, q, files := reclamationFixture(t)
	var stale *queue.Maintenance
	if err := q.Maintain(t.Context(), reclamationBinding, func(m *queue.Maintenance) error { stale = m; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := ReclaimQueueArtifacts(t.Context(), req, stale); !errors.Is(err, queue.ErrOwner) {
		t.Fatal(err)
	}
	held, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reclaim(t, req, q); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal(err)
	}
	if _, err := storelock.BeginFence(held); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := reclaim(t, req, q); err == nil {
		t.Fatal("ignored stage fence")
	}
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	// Separate unfenced fixture exercises deletion ordering and retry.
	req, q, files = reclamationFixture(t)
	var partial QueueReclamationResult
	err = q.Maintain(t.Context(), reclamationBinding, func(m *queue.Maintenance) error {
		calls := 0
		plan := &queueReclamation{proof: m, unlink: func(root *os.Root, name string) error {
			calls++
			if calls == 2 {
				return os.ErrPermission
			}
			return root.Remove(name)
		}}
		_, err := cleanupOwnedWorkspaces(t.Context(), req, func(root *os.Root, name string) error { return root.RemoveAll(name) }, plan)
		partial = plan.result
		return err
	})
	if !errors.Is(err, os.ErrPermission) || partial.RemovedArchives != 1 {
		t.Fatal(partial, err)
	}
	if _, err := os.Stat(files[2]); !os.IsNotExist(err) {
		t.Fatal("commit was not first", err)
	}
	for _, path := range files[:2] {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("capture/seed deleted early", err)
		}
	}
	result, err := reclaim(t, req, q)
	if err != nil || result.RemovedArchives != 2 {
		t.Fatal(result, err)
	}
	result, err = reclaim(t, req, q)
	if err != nil || result.RemovedArchives != 0 {
		t.Fatal(result, err)
	}
}

func TestReclaimQueueArtifactsRefusesFreshQueueHistory(t *testing.T) {
	req, _, files := reclamationFixture(t)
	fresh, err := queue.Create(t.Context(), filepath.Join(t.TempDir(), "fresh"), reclamationBinding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reclaim(t, req, fresh)
	if err != nil || result.RemovedArchives != 0 || result.ProtectionReason != "empty-history" {
		t.Fatal(result, err)
	}
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("fresh queue authorized deletion", err)
		}
	}
}
