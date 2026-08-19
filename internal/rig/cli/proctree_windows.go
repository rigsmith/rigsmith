package cli

import (
	"os/exec"
	"strconv"
)

// setProcessGroup is a no-op on Windows: there is no setpgid, and the tree is
// taken down by pid with taskkill /T instead (see killProcessTree).
func setProcessGroup(_ *exec.Cmd) {}

// killProcessTree terminates the child and every descendant. `taskkill /T`
// walks the parent-pid chain, which is Windows' equivalent of signalling a
// process group; if it isn't available the direct child is still killed, so a
// launch is never left running because the tidy path failed.
func killProcessTree(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid))
	if err := kill.Run(); err == nil {
		return nil
	}
	return c.Process.Kill()
}
