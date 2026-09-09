package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestQueueAdapterStartupChecksBeforeClaimWithoutChangingStaging(t *testing.T) {
	for _, mode := range []string{"ready", "uninitialized", "binding", "destination", "overlap", "merge", "absent-branch", "offline", "cancel-fetch"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "startup fixture")
			remoteDir := filepath.Join(t.TempDir(), "remote.git")
			git(t, filepath.Dir(remoteDir), "init", "--bare", remoteDir)
			req.Sync.Config.Remote = remoteDir
			if mode != "uninitialized" {
				seed := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
				if _, err := seed.Sync(t.Context(), req.Sync); err != nil {
					t.Fatal(err)
				}
				put(t, req.Sync.StagingDir, "untouched-staged.txt", "staged bytes")
				git(t, req.Sync.StagingDir, "add", "untouched-staged.txt")
				put(t, req.Sync.StagingDir, "untouched-staged.txt", "unstaged bytes")
			}
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
			commits := artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}
			q, dir, adapter := queueAdapterFixture(t, req, commits, remote)
			inputs := service.QueueInputs{Sync: req.Sync, Profiles: req.Profiles, Captures: req.Store, Commits: commits, Remote: remote}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fetches := 0
			offline := errors.Join(commitartifact.ErrTransport, errors.New("synthetic offline"))
			remote.beforeFetch = func() error {
				fetches++
				_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
				if err == nil {
					release()
					t.Fatal("startup did not retain staging ownership")
				}
				if !errors.Is(err, storelock.ErrBusy) {
					t.Fatal(err)
				}
				if mode == "offline" {
					return offline
				}
				if mode == "cancel-fetch" {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			var want error
			switch mode {
			case "uninitialized":
				want = commitartifact.ErrSharedHistory
			case "binding":
				inputs.Profiles = []string{"different-profile"}
				want = queue.ErrBinding
			case "destination":
				inputs.Remote, err = commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: remoteDir, Branch: "different"})
				if err != nil {
					t.Fatal(err)
				}
				want = queue.ErrBinding
			case "overlap":
				inputs.Commits.Dir = filepath.Join(req.Sync.StagingDir, "bad-scratch")
			case "merge":
				put(t, req.Sync.StagingDir, ".git/MERGE_HEAD", git(t, req.Sync.StagingDir, "rev-parse", "HEAD"))
				want = commitartifact.ErrConflict
			case "absent-branch":
				git(t, remoteDir, "update-ref", "-d", "refs/heads/main")
				want = commitartifact.ErrSharedHistory
			case "offline":
				want = offline
			case "cancel-fetch":
				want = context.Canceled
			}
			before, err := os.ReadFile(filepath.Join(dir, "queue.json"))
			if err != nil {
				t.Fatal(err)
			}
			var status, refs string
			var index []byte
			if mode != "uninitialized" {
				status = git(t, req.Sync.StagingDir, "status", "--porcelain=v1")
				refs = git(t, req.Sync.StagingDir, "show-ref")
				index, err = os.ReadFile(filepath.Join(req.Sync.StagingDir, ".git", "index"))
				if err != nil {
					t.Fatal(err)
				}
			}
			stop := make(chan struct{})
			check := func(ctx context.Context, binding queue.Binding) error {
				err := adapter.Service.CheckQueueStartup(ctx, binding, inputs)
				close(stop) // Also keep a successful preflight from claiming fixture work.
				return err
			}
			result, err := q.Run(ctx, adapter, queue.RunOptions{Stop: stop, CheckStartup: check})
			if result.CompletedBatches != 0 || (mode == "ready" && err != nil) || (mode != "ready" && err == nil) || (want != nil && !errors.Is(err, want)) {
				t.Fatal(result, err)
			}
			if mode == "overlap" && (err == nil || !strings.Contains(err.Error(), "commit store must be outside source, capture and staging roots")) {
				t.Fatal("overlap did not fail at the disjoint-store check", err)
			}
			if after, err := os.ReadFile(filepath.Join(dir, "queue.json")); err != nil || string(after) != string(before) {
				t.Fatal("startup changed queue state", err)
			}
			if mode != "uninitialized" {
				after, err := os.ReadFile(filepath.Join(req.Sync.StagingDir, ".git", "index"))
				if err != nil || string(after) != string(index) || git(t, req.Sync.StagingDir, "show-ref") != refs || git(t, req.Sync.StagingDir, "status", "--porcelain=v1") != status {
					t.Fatal("startup changed staging", err)
				}
			}
			wantFetch := mode == "ready" || mode == "absent-branch" || mode == "offline" || mode == "cancel-fetch"
			if (wantFetch && fetches != 1) || (!wantFetch && fetches != 0) || remote.pushes != 0 {
				t.Fatal("unexpected network effects", fetches, remote.pushes)
			}
			_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
			if err != nil {
				t.Fatal("leaked staging lease", err)
			}
			release()
			entries, err := os.ReadDir(commits.Dir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".startup-history-") {
					t.Fatal("leaked startup scratch")
				}
			}
		})
	}
}
