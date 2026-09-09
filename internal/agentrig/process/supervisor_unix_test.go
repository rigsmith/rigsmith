//go:build linux || darwin

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func supervisorContext(ctx context.Context) context.Context {
	return WithSupervisor(ctx, os.Args[0], "-test.run=^TestUnixSupervisorEntrypoint$")
}

func TestUnixSupervisorEntrypoint(t *testing.T) {
	// Only the explicitly selected helper invocation receives the protocol fds.
	if len(os.Args) != 2 || os.Args[1] != "-test.run=^TestUnixSupervisorEntrypoint$" {
		return
	}
	os.Exit(ServeSupervisor())
}

func TestUnixSupervisedCommand(t *testing.T) {
	for _, mode := range []string{"return", "wait", "exit", "missing", "io"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			ctx, release, err := storelock.Acquire(ctx, t.TempDir(), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx = supervisorContext(ctx)
			marker := filepath.Join(t.TempDir(), "child")
			cmd := helperCommand(mode, marker)
			if mode == "missing" {
				cmd = exec.Command(filepath.Join(t.TempDir(), "missing"))
			}
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if mode == "io" {
				cmd = exec.Command(os.Args[0], "-test.run=^TestUnixSupervisedIOHelper$", "--", "quoted argument ' \" end\xfe")
				cmd.Env = []string{"RIG_SUPERVISOR_IO=1", "CUSTOM_VALUE=provided\xff"}
				cmd.Dir = t.TempDir()
				cmd.Stdin = strings.NewReader("input")
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
			}
			if mode == "wait" {
				go func() {
					if waitMarker(marker) {
						cancel()
					}
				}()
			}
			expectedDir := cmd.Dir
			if expectedDir != "" {
				expectedDir, _ = filepath.EvalSymlinks(expectedDir)
			}
			err = Run(ctx, cmd)
			switch mode {
			case "return":
				if err != nil {
					t.Fatal(err, stderr.String())
				}
			case "io":
				if err != nil || stdout.String() != "input|provided\xff|quoted argument ' \" end\xfe|"+expectedDir || stderr.String() != "stderr" {
					t.Fatalf("command IO: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
			case "wait":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "exit":
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 7 {
					t.Fatalf("exit classification: %T %v", err, err)
				}
			case "missing":
				if err == nil || !strings.Contains(err.Error(), "missing") {
					t.Fatal(err)
				}
				if _, ok := err.(*exec.ExitError); ok {
					t.Fatal("supervisor failure looks like ordinary command exit")
				}
			}
			if mode == "return" || mode == "wait" {
				_ = os.WriteFile(marker+".released", nil, 0600)
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(marker + ".late"); !os.IsNotExist(err) {
					t.Fatal("helper wrote after completion", err)
				}
			}
		})
	}
}

func TestUnixSupervisedIOHelper(t *testing.T) {
	if os.Getenv("RIG_SUPERVISOR_IO") != "1" {
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	fmt.Fprintf(os.Stdout, "%s|%s|%s|%s", input, os.Getenv("CUSTOM_VALUE"), os.Args[len(os.Args)-1], dir)
	fmt.Fprint(os.Stderr, "stderr")
	// The protocol and lock must be close-on-exec, rather than inherited by Git.
	for fd := 3; fd <= 6; fd++ {
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); err == nil && st.Mode&syscall.S_IFMT == syscall.S_IFREG {
			os.Exit(9)
		}
	}
	os.Exit(0)
}

// The startup variant stops in the dedicated supervisor entry point before it
// reads the command. The running variant stops after a writer has started.
// Stopping the supervisor makes the cleanup interval deterministic: killing the
// worker must not release the store lock until the supervisor resumes and drains.
func TestUnixSupervisorWorkerDeath(t *testing.T) {
	for _, stage := range []string{"startup", "running"} {
		for iteration := range 2 {
			t.Run(fmt.Sprintf("%s/%d", stage, iteration), func(t *testing.T) {
				dir := t.TempDir()
				cmd := exec.Command(os.Args[0], "-test.run=^TestUnixSupervisorWorkerHelper$")
				cmd.Env = append(os.Environ(), "RIG_SUPERVISOR_WORKER="+dir, "RIG_SUPERVISOR_STAGE="+stage)
				var output strings.Builder
				cmd.Stdout, cmd.Stderr = &output, &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				waited := make(chan error, 1)
				go func() {
					waited <- cmd.Wait()
					close(waited)
				}()
				t.Cleanup(func() {
					_ = cmd.Process.Kill()
					select {
					case <-waited:
					case <-time.After(15 * time.Second):
						t.Error("worker helper did not exit during test cleanup")
					}
				})
				if !waitMarker(filepath.Join(dir, "supervisor")) {
					t.Fatal("supervisor did not start")
				}
				pidData, err := os.ReadFile(filepath.Join(dir, "supervisor"))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(pidData))
				if err != nil {
					t.Fatal(err)
				}
				if stage == "running" && !waitMarker(filepath.Join(dir, "child")) {
					t.Fatal("writer did not start")
				}
				if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
					t.Fatal(err)
				}
				// Always resume so an assertion failure cannot strand a lock holder.
				resumed := false
				t.Cleanup(func() {
					if !resumed {
						_ = os.WriteFile(filepath.Join(dir, "serve"), nil, 0600)
						_ = syscall.Kill(pid, syscall.SIGCONT)
					}
				})
				if _, rel, err := storelock.Acquire(t.Context(), filepath.Join(dir, "store"), 0); rel != nil {
					rel()
					t.Fatalf("not locked before kill: %v", err)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				// The worker can be reaped while stdout stays open in the guardian.
				// Wait for its PID to disappear before testing inherited ownership.
				until := time.Now().Add(5 * time.Second)
				for syscall.Kill(cmd.Process.Pid, 0) == nil && time.Now().Before(until) {
					time.Sleep(5 * time.Millisecond)
				}
				if syscall.Kill(cmd.Process.Pid, 0) == nil {
					t.Fatal("worker is still alive")
				}
				_, release, err := storelock.Acquire(t.Context(), filepath.Join(dir, "store"), 0)
				if release != nil {
					release()
				}
				if !errors.Is(err, storelock.ErrBusy) {
					t.Fatalf("replacement writer overlapped stopped supervisor: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "serve"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Kill(pid, syscall.SIGCONT); err != nil {
					t.Fatal(err)
				}
				resumed = true
				select {
				case <-waited:
				case <-time.After(10 * time.Second):
					t.Fatal("supervisor or writer retained output after worker death")
				}
				_, release, err = storelock.Acquire(t.Context(), filepath.Join(dir, "store"), time.Second)
				if err != nil {
					t.Fatal("store unavailable after cleanup", err, output.String())
				}
				release()
				_ = os.WriteFile(filepath.Join(dir, "child.released"), nil, 0600)
				time.Sleep(100 * time.Millisecond)
				if _, err := os.Stat(filepath.Join(dir, "child.late")); !os.IsNotExist(err) {
					t.Fatal("old writer survived lock reacquisition", err)
				}
			})
		}
	}
}

func TestUnixSupervisorWorkerHelper(t *testing.T) {
	dir := os.Getenv("RIG_SUPERVISOR_WORKER")
	if dir == "" {
		return
	}
	ctx, release, err := storelock.Acquire(context.Background(), filepath.Join(dir, "store"), 0)
	if err != nil {
		os.Exit(2)
	}
	defer release()
	ctx = WithSupervisor(ctx, os.Args[0], "-test.run=^TestUnixSupervisorStoppedEntrypoint$")
	cmd := helperCommand("wait", filepath.Join(dir, "child"))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := Run(ctx, cmd); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestUnixSupervisorStoppedEntrypoint(t *testing.T) {
	dir := os.Getenv("RIG_SUPERVISOR_WORKER")
	if dir == "" {
		return
	}
	// Atomic publication prevents the parent from reading a partial PID.
	marker := filepath.Join(dir, "supervisor")
	if err := os.WriteFile(marker+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(4)
	}
	_ = os.Rename(marker+".tmp", marker)
	if os.Getenv("RIG_SUPERVISOR_STAGE") == "startup" && !waitMarker(filepath.Join(dir, "serve")) {
		os.Exit(5)
	}
	os.Exit(ServeSupervisor())
}

func TestUnixSupervisorRejectsInvalidStartup(t *testing.T) {
	ctx, release, err := storelock.Acquire(t.Context(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		cmd  *exec.Cmd
	}{
		{"no-lease", supervisorContext(t.Context()), helperCommand("exit", "")},
		{"relative-supervisor", WithSupervisor(ctx, "relative"), helperCommand("exit", "")},
		{"missing-supervisor", WithSupervisor(ctx, filepath.Join(t.TempDir(), "missing")), helperCommand("exit", "")},
		{"oversized", supervisorContext(ctx), exec.Command(os.Args[0], strings.Repeat("x", supervisorLimit))},
		{"oversized-diagnostic", supervisorContext(ctx), &exec.Cmd{Path: strings.Repeat("\x00", 400000), Args: []string{"invalid"}}},
		{"oversized-completion", WithSupervisor(ctx, os.Args[0], "-test.run=^TestUnixSupervisorOversizedResult$"), helperCommand("exit", "")},
		{"missing-completion", WithSupervisor(ctx, os.Args[0], "-test.run=^NoMatchingTest$"), helperCommand("exit", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Run(tc.ctx, tc.cmd); err == nil {
				t.Fatal("invalid supervisor accepted")
			} else if _, ok := err.(*exec.ExitError); ok {
				t.Fatalf("supervisor failure classified as command exit: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(supervisorContext(ctx))
	cancel()
	cmd := helperCommand("exit", "")
	if err := Run(canceled, cmd); !errors.Is(err, context.Canceled) || cmd.Process != nil {
		t.Fatalf("canceled command started: %v", err)
	}
}

func TestUnixSupervisorCancelsIncompleteRequest(t *testing.T) {
	ctx, release, err := storelock.Acquire(t.Context(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease, err := storelock.Inherit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	pipe := func() (*os.File, *os.File) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
		return r, w
	}
	requestR, requestW := pipe()
	lifeR, lifeW := pipe()
	_, resultW := pipe()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixSupervisorEntrypoint$")
	cmd.ExtraFiles = []*os.File{requestR, lifeR, resultW, lease}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = requestR.Close()
	_ = lifeR.Close()
	_ = resultW.Close()
	// Leave the request writer open: EOF alone must not be needed to interrupt
	// the supervisor's request reader when the lifetime pipe has already closed.
	if _, err := requestW.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	_ = lifeW.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("incomplete request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lifetime EOF did not interrupt incomplete request")
	}
}

// This deliberately violates the trusted protocol without starting a command.
// A bounded reader must close its pipe before waiting, or this writer hangs.
func TestUnixSupervisorOversizedResult(t *testing.T) {
	if len(os.Args) != 2 || os.Args[1] != "-test.run=^TestUnixSupervisorOversizedResult$" {
		return
	}
	request := os.NewFile(3, "request")
	_, _ = io.Copy(io.Discard, request)
	_ = request.Close()
	result := os.NewFile(5, "result")
	_, _ = io.WriteString(result, strings.Repeat("x", supervisorLimit*2))
	_ = result.Close()
	os.Exit(125)
}
