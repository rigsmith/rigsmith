package service_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

var queueSyncIdentity = service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "fixture@example.com"}

func queueSyncFixture(t *testing.T) (service.SyncRequest, *queue.Queue, queue.Request, service.Service) {
	t.Helper()
	req, root := syncFixture(t, "coverage bytes")
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", remote)
	req.Config.Remote = remote
	q, r := queueSyncQueue(t, req, filepath.Join(root, "queue"))
	return req, q, r, service.Service{ReadIdentity: func() (service.Identity, error) { return queueSyncIdentity, nil }}
}

func queueSyncQueue(t *testing.T, req service.SyncRequest, dir string) (*queue.Queue, queue.Request) {
	t.Helper()
	binding, err := service.CaptureBinding(req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.Create(t.Context(), dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := service.CaptureProvenance(queueSyncIdentity)
	if err != nil {
		t.Fatal(err)
	}
	r := queue.Request{EventID: "event", SessionID: "s", ProvenanceID: provenance, Flush: queue.Flush{Mode: queue.Normal}}
	return q, r
}
func enqueueSyncRequest(t *testing.T, q *queue.Queue, r queue.Request) queue.Event {
	t.Helper()
	e, err := q.Enqueue(t.Context(), r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func pendingSyncRequests(t *testing.T, q *queue.Queue, n int) []queue.Work {
	t.Helper()
	batches, err := q.Snapshot(t.Context())
	if err != nil || len(batches) != n {
		t.Fatalf("pending: %+v %v, want %d", batches, err, n)
	}
	for _, b := range batches {
		if b.Phase != queue.Queued || b.Attempts != 0 {
			t.Fatalf("manual sync changed saved phase: %+v", b)
		}
	}
	return batches
}
