//go:build !windows

// Ported from UiChangesetCommandTests.UiCommand_WhenNotInteractive_DoesNotDispatch:
// without a TTY the ui command must fail fast instead of hanging or dispatching.
// The process runs in its own session (Setsid) so bubbletea cannot fall back to
// the test runner's controlling terminal via /dev/tty, and a hard timeout
// guards against a hang. The selected-command happy path
// (UiCommand_RunsTheSelectedCommand) is skipped: driving the bubbletea menu
// needs a real TTY.
package cmdtest

import (
	"bytes"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestUINonInteractiveFailsFast(t *testing.T) {
	dir := newWorkspace(t)

	cmd := exec.Command(changerigBin, "ui")
	cmd.Dir = dir
	cmd.Stdin = nil // /dev/null: piped, immediately-EOF stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // no controlling TTY

	if err := cmd.Start(); err != nil {
		t.Fatalf("start ui: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("ui exited 0 without a TTY, want a non-interactive error\n%s", out.String())
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("ui failed to run: %v\n%s", err, out.String())
		}
		assertContains(t, out.String(), "tty")
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("ui hung in non-interactive mode\n%s", out.String())
	}
}
