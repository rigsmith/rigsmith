package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueSyncDrainsSavedIdentitiesThenFlushes(t *testing.T) {
	f, runGit := newQueueSupervisedFixture(t, func(f *queueCommandFixture) {
		chunked := false
		f.req.Config.ChunkTranscripts = &chunked
		f.req.Config.Retention.LargeFileBytes = 1024
	})
	sessionDir := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	otherIdentity := service.Identity{AccountUUID: "22222222-2222-4222-8222-222222222222", Email: "other@example.com"}
	for _, id := range []string{"s", "other"} {
		var body strings.Builder
		for n := 0; n < 20; n++ {
			fmt.Fprintf(&body, "{\"type\":\"user\",\"sessionId\":%q,\"uuid\":%q,\"cwd\":\"/workspace/acme\",\"timestamp\":\"2026-01-02T03:04:05Z\",\"message\":{\"role\":\"user\",\"content\":\"manual queue fixture\"}}\n", id, fmt.Sprintf("%s-%d", id, n))
		}
		if err := os.WriteFile(filepath.Join(sessionDir, id+".jsonl"), []byte(body.String()), 0600); err != nil {
			t.Fatal(err)
		}
		// Establish each session under its real owner. A queued request must not
		// replace an already-recorded account with a different producer identity.
		owner := f.identity
		if id == "other" {
			owner = otherIdentity
		}
		if _, err := (service.Service{ReadIdentity: func() (service.Identity, error) { return owner, nil }}).Sync(t.Context(), f.req); err != nil {
			t.Fatal(err)
		}
	}
	store := desktop.NewStore(filepath.Join(f.req.Machine.Home, ".clauderig", "desktop"))
	if _, err := store.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	f.profiles = []string{"work"}
	f.must(t, "init")
	for _, id := range []string{"s", "other"} {
		file, err := os.OpenFile(filepath.Join(sessionDir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintf(file, "{\"type\":\"user\",\"sessionId\":%q,\"uuid\":%q,\"cwd\":\"/workspace/acme\",\"timestamp\":\"2026-01-02T03:05:05Z\",\"message\":{\"role\":\"user\",\"content\":\"tail requiring flush\"}}\n", id, id+"-tail")
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	// Dropping the initialized profile selection must not fall back to ordinary sync.
	f.profiles = nil
	if _, err := f.execute(t.Context(), "sync", "--dry-run"); !errors.Is(err, queue.ErrBinding) {
		t.Fatal(err)
	}
	f.profiles = []string{"work"}
	original := f.identity
	for _, id := range []string{"s", "other"} {
		if id == "other" {
			f.identity = otherIdentity
		}
		request := filepath.Join(t.TempDir(), "request.json")
		f.must(t, "prepare", "--session", id, "--output", request, "--flush")
		f.must(t, "enqueue", request)
	}
	f.identity = original
	beforeHead := runGit("--git-dir", f.req.Config.Remote, "rev-parse", "main")
	reads := f.reads
	checks := f.privateChecks
	dry := f.must(t, "sync", "--dry-run")
	if f.reads != reads+1 || f.privateChecks != checks+1 {
		t.Fatalf("dry-run identity/privacy reads: %d/%d, checks %d/%d", f.reads, reads, f.privateChecks, checks)
	}
	if !strings.Contains(dry, "queued requests were not acknowledged") || !strings.Contains(dry, "waiting for more content") {
		t.Fatal(dry)
	}
	if after := runGit("--git-dir", f.req.Config.Remote, "rev-parse", "main"); after != beforeHead {
		t.Fatal("dry run published", after, beforeHead)
	}
	pending, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(pending) != 2 {
		t.Fatal(pending, err)
	}
	for _, batch := range pending {
		if batch.Phase != queue.Queued || batch.Attempts != 0 {
			t.Fatal(batch)
		}
	}
	reads = f.reads
	checks = f.privateChecks
	out := f.must(t, "sync", "--flush")
	if f.reads != reads+1 || f.privateChecks != checks+4 {
		t.Fatalf("manual identity/privacy reads: %d/%d, checks %d/%d", f.reads, reads, f.privateChecks, checks)
	}
	if !strings.Contains(out, "2 queued batches completed by the worker") {
		t.Fatal(out)
	}
	pending, err = f.open(t).Snapshot(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	data := runGit("--git-dir", f.req.Config.Remote, "show", "main:cli/projects/-workspace-acme/s.jsonl")
	if !strings.Contains(data, "tail requiring flush") {
		t.Fatal("flush did not publish tail", data)
	}
	entries := ledger.LoadAll(f.req.StagingDir)
	if entries["s"].Account != original.AccountUUID || entries["other"].Account != otherIdentity.AccountUUID {
		t.Fatal("worker/manual sync replaced saved attribution")
	}
	for _, remote := range f.privateRemotes {
		if remote != f.req.Config.Remote {
			t.Fatal("privacy checked wrong remote", remote)
		}
	}
}

func TestQueueSyncRefusalsBeforeCapture(t *testing.T) {
	for _, mode := range []string{"missing-runtime", "private", "supervisor", "history", "changed-config"} {
		t.Run(mode, func(t *testing.T) {
			f := newQueueFixture(t)
			sentinel := errors.New("refused " + mode)
			if mode != "missing-runtime" {
				f.must(t, "init")
				path := filepath.Join(t.TempDir(), "request.json")
				f.must(t, "prepare", "--session", "s", "--output", path)
				f.must(t, "enqueue", path)
			}
			f.deps.identity = func() (service.Identity, error) {
				t.Fatal("refusal reached identity/capture")
				return service.Identity{}, nil
			}
			switch mode {
			case "private":
				f.deps.ensurePrivate = func(context.Context, string) error { return sentinel }
				f.deps.supervise = func(context.Context) (context.Context, error) {
					t.Fatal("privacy failure reached supervision")
					return nil, nil
				}
			case "supervisor":
				f.deps.supervise = func(context.Context) (context.Context, error) { return nil, sentinel }
			case "changed-config":
				calls := 0
				f.deps.resolve = func() (service.SyncRequest, error) {
					calls++
					req := f.req
					if calls > 1 {
						copy := *req.Config
						copy.Remote = "https://github.com/acme/changed.git"
						req.Config = &copy
					}
					return req, nil
				}
			}
			_, err := f.execute(t.Context(), "sync", "--dry-run")
			if err == nil {
				t.Fatal("accepted invalid sync")
			}
			switch mode {
			case "private", "supervisor":
				if !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
			case "missing-runtime":
				if !os.IsNotExist(err) {
					t.Fatal(err)
				}
			case "changed-config":
				if !errors.Is(err, queue.ErrBinding) {
					t.Fatal(err)
				}
			case "history":
				if !errors.Is(err, commitartifact.ErrSharedHistory) {
					t.Fatal(err)
				}
			}
			if mode != "missing-runtime" {
				pending, err := f.open(t).Snapshot(t.Context())
				if err != nil || len(pending) != 1 || pending[0].Attempts != 0 || pending[0].Phase != queue.Queued {
					t.Fatal(pending, err)
				}
			}
		})
	}
}

func TestQueueSyncRetainsRequestsArrivingDuringManualSync(t *testing.T) {
	f, _ := newQueueSupervisedFixture(t)
	f.must(t, "init")
	r := f.open(t)
	f.deps.identity = func() (service.Identity, error) {
		_, err := r.Enqueue(t.Context(), f.identity, queue.Request{EventID: "during-manual-sync", SessionID: "s", Flush: queue.Flush{Mode: queue.Normal}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return f.identity, nil
	}
	out := f.must(t, "sync")
	if !strings.Contains(out, "0 queued batches completed by the worker") {
		t.Fatal(out)
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
		t.Fatalf("manual sync completed unrelated work: %+v %v", pending, err)
	}
}

func TestQueueSyncBlockedDrainDoesNotStartManualCapture(t *testing.T) {
	f, _ := newQueueSupervisedFixture(t)
	f.must(t, "init")
	request := filepath.Join(t.TempDir(), "missing-request.json")
	f.must(t, "prepare", "--session", "missing", "--output", request)
	f.must(t, "enqueue", request)
	f.deps.identity = func() (service.Identity, error) {
		t.Fatal("blocked drain reached manual capture")
		return service.Identity{}, nil
	}
	if _, err := f.execute(t.Context(), "sync"); !errors.Is(err, queue.ErrUndrained) {
		t.Fatal(err)
	}
	pending, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Status != queue.Blocked || pending[0].Attempts != 1 {
		t.Fatalf("lost failed work: %+v %v", pending, err)
	}
}
