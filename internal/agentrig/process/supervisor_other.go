//go:build !linux && !darwin && !windows

package process

import (
	"context"
	"errors"
	"os/exec"
)

func runSupervised(ctx context.Context, cmd *exec.Cmd, _ supervisorCommand) error {
	return errors.New("command supervision is unsupported on this platform")
}

// ServeSupervisor is unavailable outside Linux and macOS. Windows uses jobs.
func ServeSupervisor() int { return 125 }
