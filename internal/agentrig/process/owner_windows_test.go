package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"

	"golang.org/x/sys/windows"
)

func windowsOwnerHelper(stage, marker string) *exec.Cmd {
	cmd := helperCommand("", marker)
	cmd.Args = []string{os.Args[0], "-test.run=^TestWindowsOwnerHelper$"}
	cmd.Env = append(cmd.Env, "RIG_WINDOWS_OWNER_STAGE="+stage)
	return cmd
}

func TestWindowsOwnerHelper(t *testing.T) {
	stage := os.Getenv("RIG_WINDOWS_OWNER_STAGE")
	if stage == "" {
		return
	}
	marker := os.Getenv("RIG_PROCESS_TEST_MARKER")
	if stage == "io" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(os.Stdout, "%s|%s|%s|%s", cwd, os.Getenv("RIG_WINDOWS_IO"), os.Args[len(os.Args)-1], data)
		fmt.Fprint(os.Stderr, "stderr")
		os.Exit(0)
	}
	if stage == "leaf" {
		if err := os.WriteFile(marker+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(marker+".tmp", marker); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if stage == "command" {
		leaf := windowsOwnerHelper("leaf", marker)
		leaf.Stdout, leaf.Stderr = os.Stdout, os.Stderr
		if err := leaf.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	cmd := windowsOwnerHelper("command", marker+".leaf")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	ctx, release, err := storelock.Acquire(context.Background(), marker+".store", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := storelock.BeginFence(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := prepare(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	anchorPID, err := windows.GetProcessId(owner.anchor)
	if err != nil {
		t.Fatal(err)
	}
	pids := []uint32{anchorPID}
	if stage != "prepared" {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if stage == "running" {
			if err := owner.started(cmd); err != nil {
				t.Fatal(err)
			}
		}
		// "started" deliberately stops before owner.started: the command and
		// its descendant must already be protected at this boundary.
		if !waitMarker(marker + ".leaf") {
			t.Fatal("leaf did not start")
		}
		data, err := os.ReadFile(marker + ".leaf")
		if err != nil {
			t.Fatal(err)
		}
		leafPID, err := strconv.ParseUint(string(data), 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, uint32(cmd.Process.Pid), uint32(leafPID))
	}
	data, _ := json.Marshal(pids)
	if err := os.WriteFile(marker+".tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(marker+".tmp", marker); err != nil {
		t.Fatal(err)
	}
	// The test kills this process. No deferred cleanup is allowed to run.
	time.Sleep(30 * time.Second)
	t.Fatal("owner was never killed")
}

func observeWindowsProcess(t *testing.T, pid uint32) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(h, 1)
		_, _ = windows.WaitForSingleObject(h, 10000)
		windows.CloseHandle(h)
	})
	return h
}

func TestWindowsOwnerDeathAtStartupBoundaries(t *testing.T) {
	for _, stage := range []string{"prepared", "started", "running"} {
		t.Run(stage, func(t *testing.T) {
			// A fresh owner can start and be killed again after the first death.
			for attempt := 0; attempt < 2; attempt++ {
				marker := filepath.Join(t.TempDir(), "processes")
				cmd := windowsOwnerHelper(stage, marker)
				var output bytes.Buffer
				cmd.Stdout, cmd.Stderr = &output, &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				if !waitMarker(marker) {
					_ = cmd.Process.Kill()
					select {
					case <-done:
						t.Fatalf("owner never became ready: %s", output.String())
					case <-time.After(10 * time.Second):
						t.Fatal("owner never became ready or exited")
					}
				}
				data, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				var pids []uint32
				if err := json.Unmarshal(data, &pids); err != nil {
					t.Fatal(err)
				}
				want := 3
				if stage == "prepared" {
					want = 1
				}
				if len(pids) != want {
					t.Fatalf("got %v, want %d processes", pids, want)
				}
				handles := make([]windows.Handle, len(pids))
				for i, pid := range pids {
					handles[i] = observeWindowsProcess(t, pid)
					if state, err := windows.WaitForSingleObject(handles[i], 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
						t.Fatalf("process %d not alive before owner death: %d %v", pid, state, err)
					}
				}
				workerHandle := observeWindowsProcess(t, uint32(cmd.Process.Pid))
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				// Reap only the worker before checking the store. Do not wait for
				// children or job accounting to drain before testing replacement.
				if state, err := windows.WaitForSingleObject(workerHandle, 10000); err != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatal("worker did not exit", state, err)
				}
				_, release, acquireErr := storelock.Acquire(t.Context(), marker+".store", 0)
				if release != nil {
					release()
				}
				if !errors.Is(acquireErr, storelock.ErrFenced) {
					t.Fatal("replacement bypassed unconfirmed cleanup", acquireErr)
				}
				for i, h := range handles {
					if state, err := windows.WaitForSingleObject(h, 10000); err != nil || state != windows.WAIT_OBJECT_0 {
						t.Fatalf("process %d survived owner death: %d %v", pids[i], state, err)
					}
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("owner output pipe remained open")
				}
			}
		})
	}
}

func TestWindowsAnchorCleanupOnStartFailure(t *testing.T) {
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing.exe"))
	owner, err := prepare(cmd)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := windows.GetProcessId(owner.anchor)
	if err != nil {
		owner.close()
		t.Fatal(err)
	}
	h := observeWindowsProcess(t, pid)
	if err := cmd.Start(); err == nil {
		owner.close()
		t.Fatal("missing executable started")
	}
	if err := owner.close(); err != nil {
		t.Fatal(err)
	}
	if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("anchor survived start failure: %d %v", state, err)
	}
}

func TestWindowsJobParentPreservesCommandIO(t *testing.T) {
	dir := t.TempDir()
	cmd := windowsOwnerHelper("io", "")
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, "RIG_WINDOWS_IO=environment value")
	arg := `argument with spaces and "quotes"`
	cmd.Args = append(cmd.Args, "--", arg)
	cmd.Stdin = strings.NewReader("stdin contents")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, cmd); err != nil {
		t.Fatal(err, stderr.String())
	}
	if want := dir + "|environment value|" + arg + "|stdin contents"; stdout.String() != want || stderr.String() != "stderr" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// A failed native cleanup must not be hidden behind an ordinary start error.
func TestWindowsStartFailureSurfacesCleanupError(t *testing.T) {
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing.exe"))
	owner, err := prepare(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the real job and anchor alive while invalidating only the ownership
	// object's handle. Otherwise the dead ParentProcess could make Start itself
	// return ERROR_INVALID_HANDLE, hiding a lost cleanup error.
	job := owner.job
	defer windows.CloseHandle(job)
	owner.job = 0
	err = runOwned(t.Context(), cmd, owner)
	var startErr *os.PathError
	if !errors.As(err, &startErr) || !errors.Is(startErr, os.ErrNotExist) || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("start and cleanup errors not both surfaced: %v", err)
	}
}
