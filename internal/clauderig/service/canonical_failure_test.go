package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

func TestCanonicalFailureStopsCaptureAndFailureJournal(t *testing.T) {
	for _, afterCapture := range []bool{false, true} {
		name := "before-capture"
		if afterCapture {
			name = "after-capture"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := gitrepo.Init(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			ctx, release, err := storelock.Acquire(t.Context(), root, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			failure := errors.New("command cleanup could not be verified")
			fail := !afterCapture
			failures := 0
			var attributesBefore []byte
			// Mark this as the service-owned selection to inject the runner's
			// failure while exercising the real nested service boundaries.
			ctx = context.WithValue(ctx, canonicalRunnerKey{}, true)
			ctx = commandrun.WithRunner(ctx, func(_ context.Context, cmd *exec.Cmd) error {
				if fail {
					failures++
					return failure
				}
				return cmd.Run()
			})
			cfg := config.Default()
			cfg.Roots = nil
			svc := Service{ReadIdentity: func() (Identity, error) {
				if !afterCapture {
					t.Fatal("capture followed uncertain repair")
				}
				return Identity{}, nil
			}, Observe: func(e Event) {
				if _, ok := e.(Captured); ok {
					attributesBefore, _ = os.ReadFile(filepath.Join(root, ".gitattributes"))
					fail = true
				}
			}}
			result, err := svc.Sync(ctx, SyncRequest{Config: cfg, StagingDir: root, Machine: config.Machine{Name: "fixture"}})
			if !errors.Is(err, failure) || failures != 1 {
				t.Fatalf("failure lost or command retried: %+v %v calls=%d", result, err, failures)
			}
			if result.Publication.Committed {
				t.Fatal("committed after uncertain cleanup")
			}
			if data, err := os.ReadFile(filepath.Join(root, ".gitattributes")); (err != nil && !os.IsNotExist(err)) || string(data) != string(attributesBefore) {
				t.Fatalf("attribute preparation followed failed probe: %q %v", data, err)
			}
			rows, err := journal.Read(root, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if afterCapture {
				want = 1
			}
			if len(rows) != want {
				t.Fatalf("unsafe deferred journal write: got %d rows, want %d", len(rows), want)
			}
		})
	}
}

func TestCanonicalMaintenanceFailureCannotBecomeSuccess(t *testing.T) {
	root := t.TempDir()
	stage, remote := filepath.Join(root, "stage"), filepath.Join(root, "remote.git")
	cmd := exec.Command("git", "init", "--bare", "-b", "main", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bare fixture: %v %s", err, out)
	}
	if _, err := gitrepo.Init(t.Context(), stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "settings.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := storelock.Acquire(t.Context(), stage, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	failure := errors.New("history index cleanup uncertain")
	callsAfterFailure := 0
	failed := false
	ctx = context.WithValue(ctx, canonicalRunnerKey{}, true)
	ctx = commandrun.WithRunner(ctx, func(_ context.Context, cmd *exec.Cmd) error {
		if failed {
			callsAfterFailure++
		}
		if cmd.Args[1] == "write-tree" {
			failed = true
			return failure
		}
		return cmd.Run()
	})
	result, err := (Service{}).Publish(ctx, PublishRequest{StagingDir: stage, Remote: remote, MachineName: "fixture", Retention: config.Default().Retention})
	if !result.Pushed || !errors.Is(err, failure) || callsAfterFailure != 0 {
		t.Fatalf("maintenance hid failure or kept executing: %+v %v later=%d", result, err, callsAfterFailure)
	}
}
