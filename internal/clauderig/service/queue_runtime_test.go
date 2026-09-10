package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueRuntimeRestartPublishesSavedProducerIdentity(t *testing.T) {
	queueRuntimeRestart(t, t.Context())
}

func queueRuntimeRestart(t *testing.T, ctx context.Context) {
	req, root := syncFixture(t, "persisted runtime bytes")
	remoteDir := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", remoteDir)
	req.Config.Remote = remoteDir
	producer := service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "producer@example.com"}
	if _, err := (service.Service{ReadIdentity: func() (service.Identity, error) { return producer, nil }}).Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	transport, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: remoteDir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &queuedTransport{ArtifactTransport: transport}
	dir := filepath.Join(root, "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	event := queue.Request{EventID: "runtime-event", SessionID: "s", Flush: queue.Flush{Mode: queue.All}}
	at := time.Now()
	accepted, err := r.Enqueue(t.Context(), producer, event, at)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { t.Fatal("read worker login"); return service.Identity{}, nil }}
	inputs := service.QueueRuntimeInputs{Sync: req, Remote: remote}
	resolve := func(context.Context) (service.QueueRuntimeInputs, error) { return inputs, nil }
	// Retain a committed phase across restart without relying on the live login or
	// transcript. The transport fails before sending, rather than confirming work.
	offline := errors.Join(commitartifact.ErrTransport, errors.New("offline"))
	remote.beforeFetch = func() error { return offline }
	result, err := r.RunOne(ctx, time.Now(), r.Adapter(svc, resolve))
	if err == nil || result.Phase != queue.Committed || result.Acknowledged {
		t.Fatal(result, err)
	}
	if err := os.RemoveAll(filepath.Join(req.Machine.Home, ".claude")); err != nil {
		t.Fatal(err)
	}
	reopened, err := service.OpenQueueRuntime(t.Context(), dir, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := reopened.Enqueue(t.Context(), producer, event, at)
	if err != nil || retried.Generation != accepted.Generation {
		t.Fatal(retried, err)
	}
	remote.beforeFetch = nil
	jobs, err := reopened.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	result, err = reopened.RunOne(ctx, jobs[0].NotBefore.Add(time.Second), reopened.Adapter(svc, resolve))
	if err != nil || !result.Acknowledged {
		t.Fatal(result, err)
	}
	data := git(t, remoteDir, "show", "main:cli/projects/-workspace-acme/s.jsonl")
	if !strings.Contains(data, "persisted runtime bytes") {
		t.Fatal("missing saved bytes")
	}
	checkout := filepath.Join(root, "verification")
	git(t, root, "clone", "--branch", "main", remoteDir, checkout)
	if got := ledger.LoadAll(checkout)["s"].Account; got != producer.AccountUUID {
		t.Fatal("lost producer attribution", got)
	}
	if err := reopened.CheckStartup(ctx, svc, inputs); err != nil {
		t.Fatal("runtime startup", err)
	}
	// The lifecycle bridge must not authorize work from another runtime, even if
	// its capture policy and event IDs are identical.
	second, err := service.CreateQueueRuntime(t.Context(), filepath.Join(root, "second"), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Enqueue(t.Context(), producer, event, at); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RunOne(ctx, time.Now(), reopened.Adapter(svc, resolve)); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("cross-lifecycle adapter", err)
	}
}
