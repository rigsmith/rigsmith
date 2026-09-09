//go:build !linux && !darwin

package process

import (
	"context"
	"os/exec"
)

func runSupervised(ctx context.Context, cmd *exec.Cmd, _ supervisorCommand) error {
	return runDirect(ctx, cmd)
}

// ServeSupervisor is unavailable outside Linux and macOS. Windows uses jobs.
func ServeSupervisor() int { return 125 }
