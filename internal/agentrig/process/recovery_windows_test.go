package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"golang.org/x/sys/windows"
)

func TestWindowsRecoveryScopeAndPhases(t *testing.T) {
	first, err := currentScope()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"prepared", "stopped", "owned", "host", "restart", "namespace", "phase", "group", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			e, err := newEvidence()
			if err != nil || e.Scope != first {
				t.Fatalf("native scope changed: %+v %v", e.Scope, err)
			}
			switch mode {
			case "stopped", "owned":
				e.State = mode
			case "restart":
				e.State = "owned"
				e.Scope.Boot = digest("synthetic previous kernel")
			case "host":
				e.Scope.Host = digest("different host")
				e.Scope.Boot = digest("different kernel")
			case "namespace":
				e.Scope.Namespace = digest("different namespace")
			case "phase":
				e.State = ""
			case "group":
				e.Group = 42
			}
			dir := filepath.Join(t.TempDir(), "store")
			ctx, release, err := storelock.Acquire(t.Context(), dir, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if mode == "legacy" {
				_, err = storelock.BeginFence(ctx)
			} else {
				_, err = storelock.BeginRecoverableFence(ctx, e.bytes())
			}
			if err != nil {
				t.Fatal(err)
			}
			if changed, err := RecoverStore(t.Context(), dir); changed || !errors.Is(err, storelock.ErrBusy) {
				t.Fatalf("recovered active owner: %v %v", changed, err)
			}
			release()
			changed, err := RecoverStore(t.Context(), dir)
			want := mode == "prepared" || mode == "stopped" || mode == "restart"
			if changed != want || (err == nil) != want {
				t.Fatalf("recovery(%s): %v %v", mode, changed, err)
			}
		})
	}
}

func TestWindowsSystemCreationSnapshotValidation(t *testing.T) {
	const header = int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	for _, mode := range []string{"valid", "truncated", "offset", "unaligned", "missing", "parent", "session", "time"} {
		t.Run(mode, func(t *testing.T) {
			data := make([]byte, header*2)
			first := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&data[0]))
			first.NextEntryOffset = uint32(header)
			second := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&data[header]))
			second.UniqueProcessID, second.CreateTime = 4, 123456
			switch mode {
			case "truncated":
				data = data[:len(data)-1]
			case "offset":
				first.NextEntryOffset = ^uint32(0)
			case "unaligned":
				first.NextEntryOffset++
			case "missing":
				second.UniqueProcessID = 99
			case "parent":
				second.InheritedFromUniqueProcessID = 99
			case "session":
				second.SessionID = 1
			case "time":
				second.CreateTime = 0
			}
			got, err := systemCreationFromSnapshot(data)
			if mode == "valid" {
				if err != nil || got != 123456 {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid kernel identity", got)
			}
		})
	}
}

func TestWindowsRecoveryCrashHelper(t *testing.T) {
	root := os.Getenv("RIG_WINDOWS_RECOVERY_ROOT")
	if root == "" {
		return
	}
	stage := os.Getenv("RIG_WINDOWS_RECOVERY_STAGE")
	ctx, release, err := storelock.Acquire(context.Background(), filepath.Join(root, "store"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	e, err := newEvidence()
	if err != nil {
		t.Fatal(err)
	}
	fence, err := storelock.BeginRecoverableFence(ctx, e.bytes())
	if err != nil {
		t.Fatal(err)
	}
	cmd := helperCommand("return", "")
	if stage == "owned" {
		cmd = windowsOwnerHelper("command", filepath.Join(root, "leaf"))
	}
	owner, err := prepare(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	anchor, err := windows.GetProcessId(owner.anchor)
	if err != nil {
		t.Fatal(err)
	}
	pids := []uint32{anchor}
	if stage != "prepared" {
		e.State = "owned"
		if err := fence.SetRecoveryEvidence(e.bytes()); err != nil {
			t.Fatal(err)
		}
		if stage == "owned" {
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if !waitMarker(filepath.Join(root, "leaf")) {
				t.Fatal("descendant did not start")
			}
			data, err := os.ReadFile(filepath.Join(root, "leaf"))
			if err != nil {
				t.Fatal(err)
			}
			var leaf uint32
			if err := json.Unmarshal(data, &leaf); err != nil {
				t.Fatal(err)
			}
			pids = append(pids, uint32(cmd.Process.Pid), leaf)
		} else {
			if err, verified := runOwnedChecked(ctx, cmd, owner); err != nil || !verified {
				t.Fatalf("job cleanup: %v %v", verified, err)
			}
			if err := sealWindowsCleanup(fence, e); err != nil {
				t.Fatal(err)
			}
			pids = nil // The production cleanup path already waited every handle.
		}
	}
	data, _ := json.Marshal(pids)
	if err := os.WriteFile(filepath.Join(root, "ready.tmp"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "ready.tmp"), filepath.Join(root, "ready")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second) // Parent kills us; no defer may clear the fence.
	os.Exit(2)
}

func TestWindowsRecoveryAfterOwnerDeath(t *testing.T) {
	for _, stage := range []string{"prepared", "owned", "stopped"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			store := filepath.Join(root, "store")
			if err := os.Mkdir(store, 0700); err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(store, "retained-capture")
			if err := os.WriteFile(retained, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsRecoveryCrashHelper$")
			cmd.Env = append(os.Environ(), "RIG_WINDOWS_RECOVERY_ROOT="+root, "RIG_WINDOWS_RECOVERY_STAGE="+stage)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if !waitMarker(filepath.Join(root, "ready")) {
				_ = cmd.Process.Kill()
				select {
				case <-done:
					t.Fatalf("worker failed: %s", output.String())
				case <-time.After(15 * time.Second):
					t.Fatal("worker did not become ready or exit")
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "ready"))
			if err != nil {
				t.Fatal(err)
			}
			var pids []uint32
			if err := json.Unmarshal(data, &pids); err != nil {
				t.Fatal(err)
			}
			var handles []windows.Handle
			for _, pid := range pids {
				handles = append(handles, observeWindowsProcess(t, pid))
			}
			if changed, err := RecoverStore(t.Context(), store); changed || !errors.Is(err, storelock.ErrBusy) {
				t.Fatalf("recovered active worker: %v %v", changed, err)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("worker did not exit")
			}
			for _, h := range handles {
				if state, err := windows.WaitForSingleObject(h, 10000); err != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatal("fixture job process survived", state, err)
				}
			}
			changed, err := RecoverStore(t.Context(), store)
			if stage == "owned" {
				// Even known fixture PIDs exiting cannot authorize an arbitrary
				// restarted worker to infer cleanup of every old descendant.
				if changed || !errors.Is(err, ErrWritersActive) {
					t.Fatalf("guessed old job cleanup: %v %v", changed, err)
				}
			} else if err != nil || !changed {
				t.Fatalf("could not recover %s: %v %v", stage, changed, err)
			}
			data, err = os.ReadFile(retained)
			if err != nil || string(data) != "preserved" {
				t.Fatalf("recovery changed capture: %q %v", data, err)
			}
		})
	}
}
