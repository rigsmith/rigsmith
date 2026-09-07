package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func helperCommand(mode, marker string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "RIG_PROCESS_TEST_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "RIG_PROCESS_TEST_MODE="+mode, "RIG_PROCESS_TEST_MARKER="+marker)
	return cmd
}
func waitMarker(path string) bool {
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
func TestProcessHelper(t *testing.T) {
	mode := os.Getenv("RIG_PROCESS_TEST_MODE")
	if mode == "" {
		return
	}
	marker := os.Getenv("RIG_PROCESS_TEST_MARKER")
	switch mode {
	case "exit":
		os.Exit(7)
	case "leaf":
		if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
			os.Exit(3)
		}
		// Only attempt the write after Run has returned. This also avoids the
		// race detector's exit delay being mistaken for a leaked descendant.
		if !waitMarker(marker + ".released") {
			os.Exit(8)
		}
		_ = os.WriteFile(marker+".late", []byte("escaped cleanup"), 0600)
		time.Sleep(30 * time.Second)
	case "return", "wait":
		child := helperCommand("leaf", marker)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		if !waitMarker(marker) {
			_ = child.Process.Kill()
			os.Exit(5)
		}
		if mode == "return" {
			os.Exit(0)
		}
		time.Sleep(30 * time.Second)
	default:
		os.Exit(6)
	}
	os.Exit(0)
}
func TestRunCleansHelpersOnExitAndCancellation(t *testing.T) {
	for _, mode := range []string{"return", "wait"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "child")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := helperCommand(mode, marker)
			// A descendant inherits this pipe; cleaning only the parent would hang Wait.
			var output strings.Builder
			cmd.Stdout = &output
			cmd.Stderr = &output
			if mode == "wait" {
				go func() {
					if waitMarker(marker) {
						cancel()
					}
				}()
			}
			start := time.Now()
			err := Run(ctx, cmd)
			if mode == "return" && err != nil {
				t.Fatal(err)
			}
			if mode == "wait" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("descendant held output pipe")
			}
			if !waitMarker(marker) {
				t.Fatal("child never started")
			}
			if err := os.WriteFile(marker+".released", []byte("returned"), 0600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
			if _, err := os.Stat(marker + ".late"); !os.IsNotExist(err) {
				t.Fatal("descendant escaped cleanup", err)
			}
		})
	}
}
func TestRunPreservesExitAndStartErrors(t *testing.T) {
	err := Run(t.Context(), helperCommand("exit", ""))
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("exit: %T %v", err, err)
	}
	if err := Run(t.Context(), exec.Command(filepath.Join(t.TempDir(), "missing"))); err == nil {
		t.Fatal("missing command accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, helperCommand("exit", "")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
