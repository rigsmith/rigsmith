package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

type queuedTransport struct {
	service.ArtifactTransport
	beforeFetch func() error
	afterPush   func()
	pushes      int
}

func (r *queuedTransport) Fetch(ctx context.Context, dir, ref string) (string, error) {
	if r.beforeFetch != nil {
		if err := r.beforeFetch(); err != nil {
			return "", err
		}
	}
	return r.ArtifactTransport.Fetch(ctx, dir, ref)
}
func (r *queuedTransport) Push(ctx context.Context, dir, commit string) error {
	r.pushes++
	err := r.ArtifactTransport.Push(ctx, dir, commit)
	if err == nil && r.afterPush != nil {
		r.afterPush()
	}
	return err
}

func queueAdapterFixture(t *testing.T, req service.ArtifactCaptureRequest, commits artifact.Store, remote service.ArtifactTransport) (*queue.Queue, string, service.QueueAdapter) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "queue")
	q, err := queue.Create(t.Context(), dir, req.Binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range req.Work.Events {
		if _, err := q.Enqueue(t.Context(), event.Request, event.EnqueuedAt); err != nil {
			t.Fatal(err)
		}
	}
	adapter := service.QueueAdapter{
		Service: service.Service{ReadIdentity: func() (service.Identity, error) { t.Fatal("read worker login"); return service.Identity{}, nil }},
		Resolve: func(_ context.Context, binding queue.Binding, provenance string) (service.QueueInputs, error) {
			if binding != req.Binding || provenance != req.Work.Events[0].Request.ProvenanceID {
				t.Fatal("wrong resolver binding")
			}
			return service.QueueInputs{Sync: req.Sync, Identity: req.Identity, Profiles: req.Profiles, Captures: req.Store, Commits: commits, Remote: remote}, nil
		},
	}
	return q, dir, adapter
}

func TestQueueAdapterOfflineRecoveryPreservesLaterGeneration(t *testing.T) {
	req := artifactCaptureFixture(t, "queued original bytes")
	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	git(t, filepath.Dir(remoteDir), "init", "--bare", remoteDir)
	req.Sync.Config.Remote = remoteDir
	var err error
	req.Binding, err = service.CaptureBinding(req.Sync, req.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: remoteDir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &queuedTransport{ArtifactTransport: transport}
	q, dir, adapter := queueAdapterFixture(t, req, artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}, remote)
	offline := errors.New("synthetic offline")
	var later queue.Event
	remote.beforeFetch = func() error {
		_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
		if err == nil {
			release()
			t.Fatal("staging lease released before publication")
		}
		if !errors.Is(err, storelock.ErrBusy) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		if _, err := (service.Service{}).Sync(ctx, req.Sync); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("manual sync was not excluded: %v", err)
		}
		request := req.Work.Events[0].Request
		request.EventID = "later-event"
		later, err = q.Enqueue(t.Context(), request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		return offline
	}
	result, err := q.RunOne(t.Context(), time.Now(), adapter)
	if !errors.Is(err, offline) || result.Phase != queue.Committed || result.Acknowledged {
		t.Fatalf("offline result: %+v %v", result, err)
	}
	before, err := q.Snapshot(t.Context())
	if err != nil || len(before) != 2 || before[0].CaptureRef == "" || before[0].CommitRef == "" {
		t.Fatalf("lost durable work: %+v %v", before, err)
	}
	_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
	if err != nil {
		t.Fatal("failure leaked staging lease", err)
	}
	release()
	q, err = queue.Open(t.Context(), dir, req.Binding)
	if err != nil {
		t.Fatal(err)
	}
	remote.beforeFetch = nil
	result, err = q.RunOne(t.Context(), time.Now(), adapter)
	if err != nil || !result.Acknowledged || result.Phase != queue.Pushed || remote.pushes != 1 {
		t.Fatalf("recovery: %+v %v pushes=%d", result, err, remote.pushes)
	}
	if data := git(t, remoteDir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(data, "queued original bytes") {
		t.Fatal("did not publish sealed bytes")
	}
	after, err := q.Snapshot(t.Context())
	if err != nil || len(after) != 1 || after[0].ID != later.BatchID || after[0].Phase != queue.Queued {
		t.Fatalf("later input incorrectly acknowledged: %+v %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(req.Sync.StagingDir, "cli")); !os.IsNotExist(err) {
		t.Fatal("worker changed canonical staging", err)
	}
}

func seedQueuePhase(t *testing.T, q *queue.Queue, req service.ArtifactCaptureRequest, phase queue.Phase) {
	t.Helper()
	w, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, err := w.Next(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, queue.Captured, req.Work.CaptureRef); err != nil {
		t.Fatal(err)
	}
	if phase == queue.Committed {
		if err := w.Progress(t.Context(), b.ID, queue.Committed, req.Work.CommitRef); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueueAdapterRecoversUnmarkedRemotePush(t *testing.T) {
	input, fixtureRemote := publicationFixture(t, true, false)
	transport, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: fixtureRemote.dir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &queuedTransport{ArtifactTransport: transport}
	req := input.Commit.Capture
	q, dir, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
	seedQueuePhase(t, q, req, queue.Committed)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	remote.afterPush = cancel
	result, err := q.RunOne(ctx, time.Now(), adapter)
	if !errors.Is(err, context.Canceled) || result.Phase != queue.Committed || result.Acknowledged || remote.pushes != 1 {
		t.Fatalf("unmarked push: %+v %v pushes=%d", result, err, remote.pushes)
	}
	q, err = queue.Open(t.Context(), dir, req.Binding)
	if err != nil {
		t.Fatal(err)
	}
	remote.afterPush = nil
	result, err = q.RunOne(t.Context(), time.Now(), adapter)
	if err != nil || !result.Acknowledged || remote.pushes != 1 {
		t.Fatalf("replayed push: %+v %v pushes=%d", result, err, remote.pushes)
	}
}

func TestQueueAdapterRefusesMissingSavedArtifacts(t *testing.T) {
	for _, phase := range []queue.Phase{queue.Captured, queue.Committed} {
		t.Run(string(phase), func(t *testing.T) {
			input, remote := publicationFixture(t, false, false)
			req := input.Commit.Capture
			q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
			seedQueuePhase(t, q, req, phase)
			store, ref := req.Store, req.Work.CaptureRef
			if phase == queue.Committed {
				store, ref = input.Commit.Commits, req.Work.CommitRef
			}
			key, _, _ := strings.Cut(ref, ":")
			path := filepath.Join(store.Dir, key+".capture")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if err == nil || result.Phase != phase || result.Acknowledged || remote.fetches != 0 {
				t.Fatalf("missing artifact accepted: %+v %v", result, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("rebuilt saved artifact", err)
			}
			_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
			if err != nil {
				t.Fatal("Begin leaked lease", err)
			}
			release()
		})
	}
}

func TestQueueAdapterValidatesResolvedInputsBeforeEffects(t *testing.T) {
	input, remote := publicationFixture(t, false, false)
	for _, kind := range []string{"configuration", "provenance", "destination", "local-only"} {
		t.Run(kind, func(t *testing.T) {
			q, _, adapter := queueAdapterFixture(t, input.Commit.Capture, input.Commit.Commits, remote)
			resolve := adapter.Resolve
			adapter.Resolve = func(ctx context.Context, binding queue.Binding, provenance string) (service.QueueInputs, error) {
				inputs, err := resolve(ctx, binding, provenance)
				cfg := *inputs.Sync.Config
				inputs.Sync.Config = &cfg
				switch kind {
				case "configuration":
					inputs.Sync.Config.Remote = "https://github.com/acme/changed"
				case "provenance":
					inputs.Identity = service.Identity{}
				case "destination":
					inputs.Remote = &artifactRemote{dir: "https://github.com/acme/changed", branch: "main"}
				case "local-only":
					inputs.Remote = nil
				}
				return inputs, err
			}
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if !errors.Is(err, queue.ErrBinding) || result.Phase != queue.Queued || result.Acknowledged || remote.fetches != 0 {
				t.Fatalf("invalid inputs accepted: %+v %v", result, err)
			}
		})
	}
}

func TestQueueAdapterHoldsLeaseAcrossPhasesAndDetachesInputs(t *testing.T) {
	input, remote := publicationFixture(t, false, false)
	req := input.Commit.Capture
	q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
	w, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	work, err := w.Next(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := adapter.Begin(t.Context(), req.Binding, work)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	// The resolver's caller-owned config cannot redirect an open execution.
	req.Sync.Config.Remote = "https://github.com/acme/changed"
	for _, phase := range []queue.Phase{queue.Queued, queue.Captured, queue.Committed} {
		_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
		if err == nil {
			release()
			t.Fatal("lease missing between phases")
		}
		if !errors.Is(err, storelock.ErrBusy) {
			t.Fatal(err)
		}
		switch phase {
		case queue.Queued:
			foreign := work
			foreign.Through++
			if _, err := execution.Capture(t.Context(), foreign); !errors.Is(err, queue.ErrTransition) {
				t.Fatal("accepted changed work", err)
			}
			work.CaptureRef, err = execution.Capture(t.Context(), work)
			work.Phase = queue.Captured
		case queue.Captured:
			work.CommitRef, err = execution.Commit(t.Context(), work)
			work.Phase = queue.Committed
		case queue.Committed:
			err = execution.Push(t.Context(), work)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	execution.Close()
	execution.Close()
	if err := execution.Push(t.Context(), work); !errors.Is(err, queue.ErrTransition) {
		t.Fatal("used closed execution", err)
	}
	_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
	if err != nil {
		t.Fatal("Close leaked staging lease", err)
	}
	release()
}

func TestQueueAdapterBlocksConflictWithoutAcknowledgement(t *testing.T) {
	input, remote := publicationFixture(t, true, false)
	req := input.Commit.Capture
	q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
	seedQueuePhase(t, q, req, queue.Committed)
	put(t, req.Sync.StagingDir, ".git/MERGE_HEAD", git(t, req.Sync.StagingDir, "rev-parse", "HEAD")+"\n")
	result, err := q.RunOne(t.Context(), time.Now(), adapter)
	if !errors.Is(err, commitartifact.ErrConflict) || result.Phase != queue.Committed || result.Acknowledged || remote.fetches != 0 {
		t.Fatalf("conflict result: %+v %v", result, err)
	}
	work, err := q.Snapshot(t.Context())
	if err != nil || len(work) != 1 || work[0].Status != queue.Blocked || work[0].FailureCode != "publication-conflict" || work[0].CommitRef != req.Work.CommitRef {
		t.Fatalf("conflict not retained and blocked: %+v %v", work, err)
	}
	if _, err := q.RunOne(t.Context(), time.Now(), adapter); !errors.Is(err, queue.ErrEmpty) {
		t.Fatal("automatically retried conflict", err)
	}
}

func TestQueueAdapterResumesSavedPhaseWithoutSources(t *testing.T) {
	for _, phase := range []queue.Phase{queue.Captured, queue.Committed} {
		t.Run(string(phase), func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			req := input.Commit.Capture
			// Captured replay must construct its commit from the retained seed;
			// committed replay only needs its self-contained commit bundle.
			if phase == queue.Captured {
				input.Commit.Commits.Dir = filepath.Join(t.TempDir(), "fresh-commits")
			} else if err := os.RemoveAll(req.Store.Dir); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(req.Sync.Machine.Home, ".claude")); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(req.Sync.StagingDir); err != nil {
				t.Fatal(err)
			}
			q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
			seedQueuePhase(t, q, req, phase)
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if err != nil || !result.Acknowledged || result.Phase != queue.Pushed {
				t.Fatalf("saved phase recovery: %+v %v", result, err)
			}
			if got := git(t, remote.dir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed publication bytes") {
				t.Fatal("lost saved capture")
			}
		})
	}
}
