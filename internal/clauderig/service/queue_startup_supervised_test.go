//go:build linux || darwin || windows

package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueAdapterStartupSupervisorLease(t *testing.T) {
	for _, mode := range []string{"unbound", "matching", "foreign", "expired", "nested-foreign"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "startup lease fixture")
			remoteDir := filepath.Join(t.TempDir(), "remote.git")
			git(t, filepath.Dir(remoteDir), "init", "--bare", remoteDir)
			req.Sync.Config.Remote = remoteDir
			svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
			if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
				t.Fatal(err)
			}
			binding, err := service.CaptureBinding(req.Sync, req.Profiles)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: remoteDir, Branch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			remote := &queuedTransport{ArtifactTransport: transport}
			fetches := 0
			remote.beforeFetch = func() error {
				fetches++
				_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
				if release != nil {
					release()
				}
				if !errors.Is(err, storelock.ErrBusy) {
					t.Fatalf("startup Git ran without staging ownership: %v", err)
				}
				return nil
			}
			ctx := canonicalSupervisor(t.Context())
			leaseDir := req.Sync.StagingDir
			release := func() {}
			if mode != "unbound" {
				if mode != "matching" {
					leaseDir = filepath.Join(t.TempDir(), "other-store")
				}
				held, end, err := storelock.Acquire(t.Context(), leaseDir, 0)
				if err != nil {
					t.Fatal(err)
				}
				release = end
				defer release()
				if mode == "matching" || mode == "nested-foreign" {
					ctx = canonicalSupervisor(held)
				}
				ctx = process.WithSupervisorLease(ctx, held)
				if mode == "expired" {
					release()
				}
			}
			indexPath := filepath.Join(req.Sync.StagingDir, ".git", "index")
			before, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			inputs := service.QueueInputs{Sync: req.Sync, Profiles: req.Profiles, Captures: req.Store,
				Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}, Remote: remote}
			err = svc.CheckQueueStartup(ctx, binding, inputs)
			want := mode == "matching" || mode == "unbound"
			wantFetches := 0
			if want {
				wantFetches = 1
			}
			if (err == nil) != want || fetches != wantFetches || remote.pushes != 0 {
				t.Fatalf("startup(%s): %v, fetches=%d, pushes=%d", mode, err, fetches, remote.pushes)
			}
			if after, err := os.ReadFile(indexPath); err != nil || string(before) != string(after) {
				t.Fatal("startup changed canonical index", err)
			}
			release()
			for _, dir := range []string{req.Sync.StagingDir, leaseDir} {
				_, end, err := storelock.Acquire(t.Context(), dir, 0)
				if err != nil {
					t.Fatal("startup leaked lease or fence", dir, err)
				}
				end()
			}
		})
	}
}
