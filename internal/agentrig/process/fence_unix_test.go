//go:build linux || darwin

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestUnixFenceWorker(t *testing.T) {
	dir := os.Getenv("RIG_FENCE_WORKER")
	if dir == "" {
		return
	}
	ctx, release, err := storelock.Acquire(context.Background(), filepath.Join(dir, "store"), 0)
	if err != nil {
		os.Exit(2)
	}
	defer release()
	ctx = WithSupervisor(ctx, os.Args[0], "-test.run=^TestUnixSupervisorStoppedEntrypoint$")
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixFencedWriter$")
	if err := Run(ctx, cmd); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestUnixFencedWriter(t *testing.T) {
	dir := os.Getenv("RIG_FENCE_WORKER")
	if dir == "" {
		return
	}
	marker := filepath.Join(dir, "writer")
	if err := os.WriteFile(marker+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	if err := os.Rename(marker+".tmp", marker); err != nil {
		os.Exit(3)
	}
	if !waitMarker(marker + ".release") {
		os.Exit(4)
	}
	if err := os.WriteFile(marker+".late", nil, 0600); err != nil {
		os.Exit(5)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestUnixFenceSurvivesSupervisorDeathWithLiveWriter(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixFenceWorker$")
	cmd.Env = append(os.Environ(), "RIG_FENCE_WORKER="+dir, "RIG_SUPERVISOR_WORKER="+dir, "RIG_SUPERVISOR_STAGE=running")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Error("worker did not exit during cleanup")
		}
	})
	readPID := func(name string) int {
		t.Helper()
		path := filepath.Join(dir, name)
		if !waitMarker(path) {
			t.Fatal(name + " did not start")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			t.Fatal(err)
		}
		return pid
	}
	supervisor := readPID("supervisor")
	// Keep an idle writer alive behind its release marker while killing the
	// supervisor. No stopped process group/job-control signal is involved.
	writer := readPID("writer")
	if err := syscall.Kill(writer, 0); err != nil {
		t.Fatal("writer not alive", err)
	}
	group, err := syscall.Getpgid(writer)
	if err != nil {
		t.Fatal(err)
	}
	groupGone := false
	t.Cleanup(func() {
		if groupGone {
			return // Recovery proved absence; this ID can now be reused.
		}
		_ = syscall.Kill(-group, syscall.SIGKILL)
		stopped, err := confirmGroupExit(func() (bool, error) { return groupAlive(group) }, 10*time.Second)
		if !stopped || err != nil {
			t.Error("writer did not stop during cleanup", err)
		}
	})
	if err := syscall.Kill(supervisor, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	// Let Run observe supervisor death before the worker exits. Killing both
	// concurrently lets the supervisor receive lifetime EOF and kill the writer
	// before its own SIGKILL takes effect, defeating this orphan fixture.
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not observe supervisor death")
	}
	store := filepath.Join(dir, "store")
	for range 2 {
		_, release, err := storelock.Acquire(t.Context(), store, time.Second)
		if release != nil {
			release()
		}
		if !errors.Is(err, storelock.ErrFenced) {
			t.Fatalf("replacement overlapped live old writer: %v", err)
		}
	}
	if recovered, err := RecoverStore(t.Context(), store); recovered || !errors.Is(err, ErrWritersActive) {
		t.Fatalf("recovered over a live orphan: %v %v", recovered, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "writer.release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !waitMarker(filepath.Join(dir, "writer.late")) {
		t.Fatal("fixture did not retain a live writer after both guardians died")
	}
	// The fixture owns this exact group; recovery itself never sends signals.
	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		recovered, err := RecoverStore(t.Context(), store)
		if recovered && err == nil {
			groupGone = true
			break
		}
		if !errors.Is(err, ErrWritersActive) || time.Now().After(deadline) {
			t.Fatalf("could not recover stopped orphan group: %v %v", recovered, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, release, err := storelock.Acquire(t.Context(), store, 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestUnixUnconfirmedSupervisorFencesNextCommand(t *testing.T) {
	for _, entrypoint := range []string{"TestUnixSupervisorOversizedResult", "NoMatchingTest"} {
		t.Run(entrypoint, func(t *testing.T) {
			store := t.TempDir()
			ctx, release, err := storelock.Acquire(t.Context(), store, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			cmd := helperCommand("exit", "")
			if err := Run(WithSupervisor(ctx, os.Args[0], "-test.run=^"+entrypoint+"$"), cmd); err == nil || cmd.Process == nil {
				t.Fatal("protocol failure fixture did not run", err)
			}
			next := helperCommand("exit", "")
			if err := Run(supervisorContext(ctx), next); !errors.Is(err, storelock.ErrFenced) || next.Process != nil {
				t.Fatal("next command bypassed failed supervisor", err)
			}
			release()
			_, free, err := storelock.Acquire(t.Context(), store, 0)
			if free != nil {
				free()
			}
			if !errors.Is(err, storelock.ErrFenced) {
				t.Fatal("restart bypassed failed supervisor", err)
			}
		})
	}
}
