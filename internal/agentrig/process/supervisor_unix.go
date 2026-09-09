//go:build linux || darwin

package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"golang.org/x/sys/unix"
)

const supervisorLimit = 2 << 20

// Byte slices use JSON base64 encoding: Unix arguments, environment and paths
// may contain non-UTF-8 bytes that JSON strings would silently replace.
type supervisorRequest struct {
	Path []byte
	Args [][]byte
	Env  [][]byte
	Dir  []byte
}

func requestStrings(values []string) [][]byte {
	out := make([][]byte, len(values))
	for i, value := range values {
		out[i] = []byte(value)
	}
	return out
}

func commandStrings(values [][]byte) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

type supervisorResult struct {
	Completed bool
	ExitCode  int
	Failure   string
}

func runSupervised(ctx context.Context, cmd *exec.Cmd, supervisor supervisorCommand) error {
	if !filepath.IsAbs(supervisor.path) {
		return errors.New("supervisor executable must be absolute")
	}
	if cmd.SysProcAttr != nil || cmd.Cancel != nil || len(cmd.ExtraFiles) != 0 || cmd.Process != nil {
		return errors.New("supervised command already has process ownership")
	}
	if cmd.Err != nil {
		return cmd.Err
	}
	request, err := json.Marshal(supervisorRequest{Path: []byte(cmd.Path), Args: requestStrings(cmd.Args), Env: requestStrings(cmd.Environ()), Dir: []byte(cmd.Dir)})
	if err != nil {
		return err
	}
	if len(request) > supervisorLimit {
		return errors.New("supervised command request exceeds size limit")
	}
	leaseContext := ctx
	if supervisor.lease != nil {
		leaseContext = supervisor.lease
	}
	lease, err := storelock.Inherit(leaseContext)
	if err != nil {
		return err
	}
	defer lease.Close()
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}()
	pipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err == nil {
			opened = append(opened, r, w)
		}
		return r, w, err
	}
	requestR, requestW, err := pipe()
	if err != nil {
		return err
	}
	lifeR, lifeW, err := pipe()
	if err != nil {
		return err
	}
	resultR, resultW, err := pipe()
	if err != nil {
		return err
	}
	// Keep Go's stdio copying and ProcessState on the caller's command. Its exit
	// status is the supervised command's only after a valid completion record.
	cmd.Path = supervisor.path
	cmd.Args = append([]string{supervisor.path}, supervisor.args...)
	cmd.Env = os.Environ()
	cmd.Dir = ""
	cmd.ExtraFiles = []*os.File{requestR, lifeR, resultW, lease}
	// A separate session also avoids orphaned stopped-group SIGHUP when the
	// worker dies (and terminal job-control signals aimed at the worker).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := ctx.Err(); err != nil {
		return err
	}
	fence, err := storelock.BeginFence(leaseContext)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.Join(err, fence.Clear())
	}
	_ = requestR.Close()
	_ = lifeR.Close()
	_ = resultW.Close()
	_ = lease.Close()
	written := make(chan error, 1)
	go func() {
		_, err := requestW.Write(request)
		_ = requestW.Close()
		written <- err
	}()
	// Cancellation closes only the lifetime pipe. Killing the supervisor would
	// release its inherited lease before it could terminate/drain the Git group.
	stop := context.AfterFunc(ctx, func() { _ = lifeW.Close() })
	defer stop()
	resultBytes, readErr := io.ReadAll(io.LimitReader(resultR, supervisorLimit+1))
	// Unblock a writer that exceeds the protocol limit before waiting for it.
	_ = resultR.Close()
	waitErr := cmd.Wait()
	writeErr := <-written
	var result supervisorResult
	if readErr != nil || len(resultBytes) > supervisorLimit || json.Unmarshal(resultBytes, &result) != nil || !result.Completed {
		return errors.Join(ctx.Err(), waitErr, writeErr, errors.New("command supervisor did not confirm completion"), readErr)
	}
	if result.Failure != "" {
		return errors.Join(ctx.Err(), waitErr, writeErr, fmt.Errorf("command supervisor: %s", result.Failure))
	}
	if cmd.ProcessState.ExitCode() != result.ExitCode || writeErr != nil {
		return errors.Join(ctx.Err(), waitErr, writeErr, errors.New("command supervisor completion mismatch"))
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), waitErr)
	}
	// Preserve direct *exec.ExitError for ordinary Git exit-code classifiers.
	return waitErr
}

// ServeSupervisor runs exactly one trusted command using descriptors explicitly
// supplied by runSupervised: request, parent lifetime, completion, staging lease.
// Call only from a dedicated executable entry point and immediately os.Exit with
// its result. The supervisor must remain alive to handle worker death. Killing
// the supervisor itself leaves a persistent fence that blocks new writers.
func ServeSupervisor() int {
	files := make([]*os.File, 4)
	for i := range files {
		fd := uintptr(3 + i)
		var stat unix.Stat_t
		if err := unix.Fstat(int(fd), &stat); err != nil {
			return 125
		}
		kind := uint32(stat.Mode) & unix.S_IFMT
		if (i < 3 && kind != unix.S_IFIFO) || (i == 3 && kind != unix.S_IFREG) {
			return 125
		}
		// Make inherited pipes pollable so cancellation/Close can interrupt IO.
		if i < 3 {
			if err := unix.SetNonblock(int(fd), true); err != nil {
				return 125
			}
		}
		// Control descriptors and the lease must never pass to Git or its helpers.
		syscall.CloseOnExec(int(fd))
		files[i] = os.NewFile(fd, "agentrig-supervisor")
		defer files[i].Close()
	}
	fence, err := storelock.InheritedFence(files[3])
	if err != nil {
		return 125
	}
	clean := true // No command exists yet.
	requestR, lifeR, resultW := files[0], files[1], files[2]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		var b [1]byte
		_, _ = lifeR.Read(b[:])
		cancel()
		_ = requestR.Close() // Also interrupt an incomplete startup request.
	}()
	finish := func(code int, err error) int {
		// Clear before reporting completion, including when the worker is dead
		// and its completion reader is gone. Never clear on uncertain cleanup.
		if clean {
			if clearErr := fence.Clear(); clearErr != nil {
				err = errors.Join(err, fmt.Errorf("clear command fence: %w", clearErr))
				code = 125
			}
		}
		result := supervisorResult{Completed: true, ExitCode: code}
		if err != nil {
			result.Failure = err.Error()
		}
		data, err := json.Marshal(result)
		if err != nil {
			return 125
		}
		if len(data) > supervisorLimit {
			code = 125
			data, _ = json.Marshal(supervisorResult{Completed: true, ExitCode: code,
				Failure: "command failed; diagnostic exceeds size limit"})
		}
		if _, err := resultW.Write(data); err != nil {
			return 125
		}
		return code
	}
	data, err := io.ReadAll(io.LimitReader(requestR, supervisorLimit+1))
	if err != nil {
		return finish(125, err)
	}
	var request supervisorRequest
	if len(data) > supervisorLimit || json.Unmarshal(data, &request) != nil || len(request.Path) == 0 || len(request.Args) == 0 {
		return finish(125, errors.New("invalid supervised command request"))
	}
	if err := ctx.Err(); err != nil {
		return finish(125, err)
	}
	// Path was already resolved by the worker; never search the supervisor's PATH.
	cmd := &exec.Cmd{Path: string(request.Path), Args: commandStrings(request.Args), Env: commandStrings(request.Env), Dir: string(request.Dir),
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	clean = false
	err, clean = runDirectChecked(ctx, cmd)
	if err == nil {
		return finish(0, nil)
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
		return finish(exit.ExitCode(), nil)
	}
	return finish(125, err)
}
