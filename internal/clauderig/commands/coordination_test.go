package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/spf13/cobra"
)

func TestMutatingCommandsWaitBeforeReadingOrChangingStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	staging, err := config.StagingDir()
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := storelock.Acquire(t.Context(), staging, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, cmd := range []*cobra.Command{NewRestoreCmd(), NewMergeCmd(), newRepoGCCmd(), newRepoPruneCmd(), newLedgerBackfillCmd(), newDeviceRemoveCmd()} {
		t.Run(cmd.Name(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			cmd.SetContext(ctx)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.RunE(cmd, []string{"fixture"}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("operation entered busy store: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("rendered before ownership: %s", &out)
			}
		})
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("created store while busy: %v", err)
	}
}

func TestHookCommandsSkipBusyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	staging, err := config.StagingDir()
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := storelock.Acquire(t.Context(), staging, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, cmd := range []*cobra.Command{NewSyncCmd(), NewPullCmd()} {
		t.Run(cmd.Name(), func(t *testing.T) {
			if cmd.Name() == "sync" {
				if err := cmd.Flags().Set("hook", "true"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			cmd.SetContext(ctx)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("best-effort hook: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("hook waited for busy store")
			}
			if out.Len() == 0 {
				t.Fatal("busy skip was silent")
			}
		})
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("hook wrote while busy: %v", err)
	}
}

func TestLegacySyncWaitHonorsCancellation(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	held, got, err := acquireSyncLock(staging)
	if err != nil || !got {
		t.Fatalf("fixture: %v", err)
	}
	defer held.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	lock, got, err := acquireSyncLockWaitContext(ctx, staging, time.Hour)
	if lock != nil {
		lock.Release()
	}
	if got || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("legacy wait: got=%v err=%v", got, err)
	}
}
