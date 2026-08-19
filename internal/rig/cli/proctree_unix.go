//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the child as the leader of a new process group, so
// everything it spawns shares one killable id. Without this, killing a launch
// that exec'd or forked (`go run .`, `npm run dev`) leaves the descendant alive.
func setProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// killProcessTree signals the child's whole process group. A negative pid means
// "the group" to kill(2). Falls back to the single process when the group can't
// be resolved (the child exited between the check and the signal), so a launch
// is never left running because the tidy path failed.
func killProcessTree(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	pid := c.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
		// Only signal the group we created — never a group we don't own.
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return c.Process.Kill()
}
