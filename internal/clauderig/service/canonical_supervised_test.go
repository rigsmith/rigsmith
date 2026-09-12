//go:build linux || darwin || windows

package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestCanonicalSupervisorEntrypoint(t *testing.T) {
	if len(os.Args) == 2 && os.Args[1] == "-test.run=^TestCanonicalSupervisorEntrypoint$" {
		if os.Getenv("RIG_TEST_CANONICAL_SUPERVISOR_FAILURE") == "1" {
			os.Exit(23)
		}
		os.Exit(process.ServeSupervisor())
	}
}

func TestCanonicalRejectsExpiredOrUnrelatedSupervisorLease(t *testing.T) {
	for _, expired := range []bool{false, true} {
		req, root := syncFixture(t, "untouched")
		other, release, err := storelock.Acquire(t.Context(), filepath.Join(root, "other-store"), 0)
		if err != nil {
			t.Fatal(err)
		}
		if expired {
			release()
		}
		ctx := process.WithSupervisorLease(canonicalSupervisor(other), other)
		_, err = (service.Service{}).Capture(ctx, req)
		release()
		if err == nil {
			t.Fatal("rebound an invalid explicit supervisor lease")
		}
		if _, err := os.Stat(req.StagingDir); !os.IsNotExist(err) {
			t.Fatalf("capture wrote before lease validation: %v", err)
		}
	}
}

func canonicalSupervisor(ctx context.Context) context.Context {
	return process.WithSupervisor(ctx, os.Args[0], "-test.run=^TestCanonicalSupervisorEntrypoint$")
}

func TestCanonicalSupervisedWorkflows(t *testing.T) {
	req, q, request, svc := queueSyncFixture(t)
	enqueueSyncRequest(t, q, request)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	ctx = canonicalSupervisor(ctx)
	result, err := svc.Sync(ctx, req)
	if err != nil || !result.Publication.Pushed {
		t.Fatalf("supervised sync: %+v %v", result, err)
	}
	pendingSyncRequests(t, q, 1)
	repo, err := gitrepo.Open(t.Context(), req.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	if repaired := svc.RepairMerge(ctx, req.StagingDir, false); !repaired.Safe || repaired.Err != nil {
		t.Fatalf("supervised repair: %+v", repaired)
	}
	held, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	matching := process.WithSupervisorLease(canonicalSupervisor(held), held)
	if r := svc.RepairMerge(matching, req.StagingDir, false); !r.Safe || r.Err != nil {
		release()
		t.Fatalf("matching explicit lease: %+v", r)
	}
	release()
	if err := service.FinishMerge(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reconcile(ctx, service.ReconcileRequest{Repo: repo, Remote: "origin", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	git(t, req.Config.Remote, "symbolic-ref", "HEAD", "refs/heads/main")
	clone := filepath.Join(t.TempDir(), "clone")
	pulled := svc.Pull(ctx, service.PullRequest{Config: req.Config, Machine: req.Machine, StagingDir: clone})
	if pulled.CommandError != nil || pulled.CoordinationError != nil || pulled.RequestError != nil || pulled.CloneError != nil || pulled.ReconcileError != nil || pulled.RestoreError != nil {
		t.Fatalf("supervised pull: %+v", pulled)
	}
	if git(t, clone, "rev-parse", "HEAD") != git(t, req.StagingDir, "rev-parse", "HEAD") {
		t.Fatal("clone differs from confirmed publication")
	}
	for _, dir := range []string{req.StagingDir, clone} {
		_, release, err := storelock.Acquire(t.Context(), dir, 0)
		if err != nil {
			t.Fatalf("workflow left store unavailable: %v", err)
		}
		release()
	}
}

func TestCanonicalSupervisedConflictRepair(t *testing.T) {
	root := fixture(t)
	repo, err := gitrepo.Init(t.Context(), filepath.Join(root, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	const path = "cli/projects/-workspace-acme/memory/MEMORY.md"
	put(t, repo.Dir, path, "base\n")
	git(t, repo.Dir, "add", ".")
	git(t, repo.Dir, "commit", "-m", "base")
	git(t, repo.Dir, "checkout", "-b", "incoming")
	put(t, repo.Dir, path, "base\nincoming\n")
	git(t, repo.Dir, "commit", "-am", "incoming")
	git(t, repo.Dir, "checkout", "main")
	put(t, repo.Dir, path, "base\nlocal\n")
	git(t, repo.Dir, "commit", "-am", "local")
	if conflicted, err := repo.MergeRefUncommitted(t.Context(), "incoming"); err != nil || !conflicted {
		t.Fatalf("conflict fixture: %v %v", conflicted, err)
	}
	ctx, cancel := context.WithTimeout(canonicalSupervisor(t.Context()), 2*time.Minute)
	defer cancel()
	repaired := (service.Service{}).RepairMerge(ctx, repo.Dir, false)
	if !repaired.Safe || repaired.Err != nil {
		t.Fatalf("supervised conflict repair: %+v", repaired)
	}
	body := git(t, repo.Dir, "show", "HEAD:"+path)
	if !strings.Contains(body, "local") || !strings.Contains(body, "incoming") {
		t.Fatalf("repair lost one side: %q", body)
	}
	if len(strings.Fields(git(t, repo.Dir, "rev-list", "--parents", "-n", "1", "HEAD"))) != 3 {
		t.Fatal("repair did not finish merge commit")
	}
	_, release, err := storelock.Acquire(t.Context(), repo.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCanonicalSupervisionRejectsInteractiveBeforeEffects(t *testing.T) {
	req, root := syncFixture(t, "unchanged")
	req.StagingDir = filepath.Join(root, "uncreated-parent", "staging")
	req.AllowMergeTool = true
	svc := service.Service{}
	repo := &gitrepo.Repo{Dir: req.StagingDir}
	ctx := canonicalSupervisor(t.Context())
	cases := map[string]func() error{
		"sync":    func() error { _, err := svc.Sync(ctx, req); return err },
		"capture": func() error { _, err := svc.Capture(ctx, req); return err },
		"publish": func() error {
			_, err := svc.Publish(ctx, service.PublishRequest{StagingDir: req.StagingDir, AllowMergeTool: true})
			return err
		},
		"reconcile": func() error { return svc.Reconcile(ctx, service.ReconcileRequest{Repo: repo, AllowMergeTool: true}) },
		"repair":    func() error { return svc.RepairMerge(ctx, req.StagingDir, true).Err },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, service.ErrSupervisedMergeToolUnavailable) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(req.StagingDir)); !os.IsNotExist(err) {
				t.Fatalf("rejected operation created store parent: %v", err)
			}
		})
	}
}

func TestCanonicalSupervisedRuntimeSync(t *testing.T) {
	req, _, event, svc := queueSyncFixture(t)
	runtime, err := service.CreateQueueRuntime(t.Context(), filepath.Join(t.TempDir(), "runtime"), req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Enqueue(t.Context(), queueSyncIdentity, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	result, err := runtime.Sync(canonicalSupervisor(ctx), svc, req)
	if err != nil || !result.Publication.Pushed {
		t.Fatalf("supervised runtime sync: %+v %v", result, err)
	}
	pending, err := runtime.Snapshot(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
}
