package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestCanonicalGitBoundariesRejectForeignRunner(t *testing.T) {
	req, root := syncFixture(t, "unchanged live bytes")
	staging := filepath.Join(root, "uncreated-parent", "store")
	req.StagingDir = staging
	svc := service.Service{ReadIdentity: func() (service.Identity, error) {
		t.Fatal("gated call reached capture")
		return service.Identity{}, nil
	}}
	ctx := commandrun.WithRunner(t.Context(), nil)
	repo := &gitrepo.Repo{Dir: staging}
	cases := map[string]func(context.Context) error{
		"capture": func(ctx context.Context) error { _, err := svc.Capture(ctx, req); return err },
		"sync":    func(ctx context.Context) error { _, err := svc.Sync(ctx, req); return err },
		"publish": func(ctx context.Context) error {
			_, err := svc.Publish(ctx, service.PublishRequest{StagingDir: staging})
			return err
		},
		"pull": func(ctx context.Context) error {
			return svc.Pull(ctx, service.PullRequest{Config: req.Config, Machine: req.Machine, StagingDir: staging}).RequestError
		},
		"reconcile":    func(ctx context.Context) error { return svc.Reconcile(ctx, service.ReconcileRequest{Repo: repo}) },
		"finish-merge": func(ctx context.Context) error { return service.FinishMerge(ctx, repo) },
		"repair-merge": func(ctx context.Context) error {
			r := svc.RepairMerge(ctx, staging, false)
			if r.Safe {
				t.Error("rejected repair reported safe")
			}
			return r.Err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(ctx); !errors.Is(err, service.ErrCanonicalRunnerRequired) {
				t.Fatalf("canonical boundary bypassed supervision: %v", err)
			}
			// Acquire would create this parent and its sibling lock, even before Git.
			if _, err := os.Stat(filepath.Dir(staging)); !os.IsNotExist(err) {
				t.Fatalf("rejected boundary changed staging parent: %v", err)
			}
		})
	}
}
