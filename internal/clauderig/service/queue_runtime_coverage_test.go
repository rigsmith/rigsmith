package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueRuntimeManualCoveragePreservesLaterArrivalsAndOtherLifecycles(t *testing.T) {
	req, _, event, svc := coverageFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	original, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "other"), req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	svc.ReadIdentity = func() (service.Identity, error) { reads++; return coverageIdentity, nil }
	var late queue.Event
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Captured); !ok {
			return
		}
		if _, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0); err == nil {
			release()
			t.Fatal("staging lease released")
		}
		if err := r.RetryBlocked(t.Context(), original.BatchID); !errors.Is(err, storelock.ErrBusy) {
			t.Fatalf("worker not excluded: %v", err)
		}
		next := event
		next.EventID = "later"
		late, err = r.Enqueue(t.Context(), coverageIdentity, next, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.SyncWithCoverage(t.Context(), svc, req)
	if err != nil || !reflect.DeepEqual(result.Acknowledged, []uint64{original.Generation}) || reads != 1 {
		t.Fatalf("result=%+v err=%v reads=%d", result, err, reads)
	}
	reopened, err := service.OpenQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := reopened.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != late.BatchID {
		t.Fatalf("later arrival: %+v %v", pending, err)
	}
	pending, err = other.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued {
		t.Fatalf("other lifecycle: %+v %v", pending, err)
	}
}

func TestQueueRuntimeManualCoverageRefusesInvalidBindingBeforeCapture(t *testing.T) {
	for _, mode := range []string{"configuration", "metadata", "remote", "profiles"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := coverageFixture(t)
			dir := filepath.Join(t.TempDir(), "runtime")
			profiles := engine.LocalProfileNames()
			if mode == "profiles" {
				profiles = append(profiles, "selected")
			}
			r, err := service.CreateQueueRuntime(t.Context(), dir, req, profiles)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "configuration":
				req.Machine.Name += "-changed"
			case "metadata":
				if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "remote":
				req.Config.Remote += "-changed"
			}
			svc.ReadIdentity = func() (service.Identity, error) {
				t.Fatal("invalid runtime reached capture")
				return service.Identity{}, nil
			}
			result, err := r.SyncWithCoverage(t.Context(), svc, req)
			if err == nil || len(result.Acknowledged) != 0 {
				t.Fatalf("invalid runtime accepted: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualCoverageKeepsUnprovenWork(t *testing.T) {
	for _, mode := range []string{"dry-run", "other-identity", "identity-error", "missing-source"} {
		t.Run(mode, func(t *testing.T) {
			req, _, event, svc := coverageFixture(t)
			r, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, engine.LocalProfileNames())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "dry-run":
				req.DryRun = true
			case "other-identity":
				svc.ReadIdentity = func() (service.Identity, error) { return service.Identity{Email: "other@example.com"}, nil }
			case "identity-error":
				svc.ReadIdentity = func() (service.Identity, error) { return service.Identity{}, errors.New("identity unavailable") }
			case "missing-source":
				if err := os.Remove(filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			result, err := r.SyncWithCoverage(t.Context(), svc, req)
			if err != nil || len(result.Acknowledged) != 0 {
				t.Fatalf("unproven work: %+v %v", result, err)
			}
			pending, err := r.Snapshot(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Phase != queue.Queued || pending[0].Attempts != 0 {
				t.Fatalf("work mutated: %+v %v", pending, err)
			}
		})
	}
}

func TestQueueRuntimeManualCoverageRevalidatesBeforePreparing(t *testing.T) {
	req, _, event, svc := coverageFixture(t)
	dir := filepath.Join(t.TempDir(), "runtime")
	r, err := service.CreateQueueRuntime(t.Context(), dir, req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(t.Context(), coverageIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	svc.ReadIdentity = func() (service.Identity, error) {
		if err := os.WriteFile(filepath.Join(dir, "runtime.json"), []byte("changed after preflight"), 0600); err != nil {
			t.Fatal(err)
		}
		return coverageIdentity, nil
	}
	result, err := r.SyncWithCoverage(t.Context(), svc, req)
	if err == nil || len(result.Acknowledged) != 0 {
		t.Fatalf("changed metadata accepted: %+v %v", result, err)
	}
	pending, err := r.Snapshot(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatalf("work mutated: %+v %v", pending, err)
	}
}
