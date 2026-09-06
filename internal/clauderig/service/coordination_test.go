package service_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestServiceContentionPrecedesCaptureCloneAndJournal(t *testing.T) {
	req, _ := syncFixture(t, "fixture")
	_, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	svc := service.Service{ReadIdentity: func() (service.Identity, error) {
		t.Fatal("identity observed before acquiring ownership")
		return service.Identity{}, nil
	}}
	pull := svc.Pull(t.Context(), service.PullRequest{Config: req.Config, Machine: req.Machine, StagingDir: req.StagingDir})
	if !errors.Is(pull.CoordinationError, storelock.ErrBusy) {
		t.Fatalf("pull: %+v", pull)
	}
	for name, run := range map[string]func(context.Context) error{
		"sync": func(ctx context.Context) error { _, err := svc.Sync(ctx, req); return err },
		"publish": func(ctx context.Context) error {
			_, err := svc.Publish(ctx, service.PublishRequest{StagingDir: req.StagingDir})
			return err
		},
		"reconcile": func(ctx context.Context) error {
			return svc.Reconcile(ctx, service.ReconcileRequest{Repo: &gitrepo.Repo{Dir: req.StagingDir}})
		},
		"repair": func(ctx context.Context) error {
			r := svc.RepairMerge(ctx, req.StagingDir, false)
			if r.Safe {
				t.Error("busy store declared safe")
			}
			return r.Err
		},
		"finish": func(ctx context.Context) error { return service.FinishMerge(ctx, &gitrepo.Repo{Dir: req.StagingDir}) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			if err := run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("contended operation: %v", err)
			}
		})
	}
	if _, err := os.Stat(req.StagingDir); !os.IsNotExist(err) {
		t.Fatalf("busy operation created staging: %v", err)
	}
}

func TestSyncHoldsOwnershipAcrossAllPhasesAndReleasesOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			req, _ := syncFixture(t, "fixture")
			if fail {
				req.Flush.Mode = service.FlushMode(99)
			}
			events := 0
			svc := service.Service{Observe: func(e service.Event) {
				events++
				_, end, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
				if end != nil {
					end()
				}
				if !errors.Is(err, storelock.ErrBusy) {
					t.Fatalf("event %T did not own store: %v", e, err)
				}
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_, err := svc.Sync(ctx, req)
			if (err != nil) != fail {
				t.Fatalf("sync: %v", err)
			}
			if events == 0 {
				t.Fatal("no phase observed")
			}
			_, end, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
			if err != nil {
				t.Fatalf("ownership leaked: %v", err)
			}
			end()
		})
	}
}
