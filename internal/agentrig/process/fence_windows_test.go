package process

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestWindowsSupervisedFenceCompletion(t *testing.T) {
	for _, mode := range []string{"return", "wait", "exit", "missing"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			ctx, release, err := storelock.Acquire(ctx, filepath.Join(dir, "store"), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx = WithSupervisor(ctx, "unused-on-windows")
			marker := filepath.Join(dir, "child")
			cmd := helperCommand(mode, marker)
			if mode == "missing" {
				cmd = exec.Command(filepath.Join(dir, "missing.exe"))
			}
			if mode == "wait" {
				go func() {
					if waitMarker(marker) {
						cancel()
					}
				}()
			}
			err = Run(ctx, cmd)
			switch mode {
			case "return":
				if err != nil {
					t.Fatal(err)
				}
			case "wait":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "missing":
				if err == nil {
					t.Fatal("missing executable succeeded")
				}
			case "exit":
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
					t.Fatalf("exit classification: %T %v", err, err)
				}
			}
			release()
			_, next, err := storelock.Acquire(t.Context(), filepath.Join(dir, "store"), 0)
			if err != nil {
				t.Fatal("verified cleanup retained fence", err)
			}
			next()
		})
	}
}
