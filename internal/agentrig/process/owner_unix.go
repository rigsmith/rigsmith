//go:build linux || darwin

package process

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ownership struct {
	mu       sync.Mutex
	pid      int
	finished bool
}

func prepare(cmd *exec.Cmd) (*ownership, error) {
	if cmd.SysProcAttr != nil || cmd.Cancel != nil {
		return nil, errors.New("command already has process ownership")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &ownership{}, nil
}
func (o *ownership) started(cmd *exec.Cmd) error { o.pid = cmd.Process.Pid; return nil }
func (o *ownership) wait() error                 { return waitExited(o.pid) }
func (o *ownership) stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finished {
		return nil
	}
	return killGroup(o.pid)
}
func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if errors.Is(err, syscall.EPERM) {
		// Darwin reports EPERM for a group consisting only of zombies.
		alive, checkErr := groupAlive(pid)
		if checkErr == nil && !alive {
			return nil
		}
	}
	return err
}
func (o *ownership) finish() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	// waitExited leaves the leader waitable, pinning its PID while signalling the
	// group. Disable further signals before cmd.Wait reaps it and permits PID reuse.
	err := killGroup(o.pid)
	if err == nil {
		for {
			alive, checkErr := groupAlive(o.pid)
			if checkErr != nil {
				err = checkErr
				break
			}
			if !alive {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	o.finished = true
	return err
}
func (o *ownership) close() error { return nil }
