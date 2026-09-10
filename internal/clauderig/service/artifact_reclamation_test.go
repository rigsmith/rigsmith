package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestReclaimQueueArtifactsPreservesUnacknowledgedCapture(t *testing.T) {
	req := artifactCaptureFixture(t, "saved before phase acknowledgement")
	svc := service.Service{}
	ref, err := svc.CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(t.Context(), filepath.Join(filepath.Dir(req.Store.Dir), "queue"), req.Binding)
	if err != nil {
		t.Fatal(err)
	}
	inputs := service.QueueInputs{Sync: req.Sync, Profiles: req.Profiles, Captures: req.Store, Commits: artifact.Store{Dir: filepath.Join(filepath.Dir(req.Store.Dir), "commits")}}
	if err := os.MkdirAll(req.Sync.StagingDir, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, q.Directory(), ".confirmation-interrupted/file", "scratch")
	before, err := os.ReadFile(filepath.Join(q.Directory(), "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.ReclaimQueueArtifacts(t.Context(), req.Binding, inputs, q)
	if err != nil || result.RemovedWorkspaces != 1 || result.RemovedArchives != 0 || result.ProtectionReason != "pending-work" {
		t.Fatal(result, err)
	}
	after, err := os.ReadFile(filepath.Join(q.Directory(), "queue.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("changed unfinished queue", err)
	}
	if err := req.Store.Verify(t.Context(), ref); err != nil {
		t.Fatal("lost unacknowledged output", err)
	}
	// Native sources can disappear: recovery must still reuse the exact archive.
	if err := os.RemoveAll(filepath.Join(req.Sync.Machine.Home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.CaptureArtifact(t.Context(), req); err != nil || got != ref {
		t.Fatal("recaptured saved work", got, err)
	}
	w, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	work, err := w.Next(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, progress := range []struct {
		phase queue.Phase
		ref   string
	}{{queue.Captured, ref}, {queue.Committed, "fixture-commit"}, {queue.Pushed, ""}} {
		if err := w.Progress(t.Context(), work.ID, progress.phase, progress.ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Acknowledge(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	w.Close()
	result, err = svc.ReclaimQueueArtifacts(t.Context(), req.Binding, inputs, q)
	if err != nil || result.RemovedArchives != 1 {
		t.Fatal(result, err)
	}
	if err := req.Store.Verify(t.Context(), ref); !os.IsNotExist(err) {
		t.Fatal("completed archive retained", err)
	}
	if _, err := os.Stat(inputs.Commits.Dir); !os.IsNotExist(err) {
		t.Fatal("created missing store", err)
	}
}

func TestReclaimQueueArtifactsRejectsForeignQueueAndLayouts(t *testing.T) {
	for _, mode := range []string{"binding", "source", "capture", "staging", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "fixture")
			inputs := service.QueueInputs{Sync: req.Sync, Profiles: req.Profiles, Captures: req.Store, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
			dir := filepath.Join(filepath.Dir(req.Store.Dir), "queue")
			binding := req.Binding
			switch mode {
			case "binding":
				binding.ConfigID = "other"
				dir = filepath.Join(t.TempDir(), "foreign")
			case "source":
				dir = filepath.Join(req.Sync.Machine.Home, ".claude", "queue")
			case "capture":
				dir = filepath.Join(req.Store.Dir, "queue")
			case "staging":
				dir = filepath.Join(req.Sync.StagingDir, "queue")
			}
			q, err := queue.Create(t.Context(), dir, binding)
			if err != nil {
				t.Fatal(err)
			}
			put(t, dir, ".confirmation-retained/file", "keep")
			if mode == "corrupt" {
				put(t, dir, "queue.json", "corrupt")
			}
			result, err := (service.Service{}).ReclaimQueueArtifacts(t.Context(), req.Binding, inputs, q)
			if err == nil || result.RemovedWorkspaces != 0 || result.RemovedArchives != 0 {
				t.Fatal(result, err)
			}
			if mode != "corrupt" && !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(dir, ".confirmation-retained/file")); err != nil || string(b) != "keep" {
				t.Fatal(err)
			}
		})
	}
}
