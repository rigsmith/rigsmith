package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func runSupervised(ctx context.Context, cmd *exec.Cmd, supervisor supervisorCommand) error {
	lease := ctx
	if supervisor.lease != nil {
		lease = supervisor.lease
	}
	// Intent must precede preparation: preparation itself creates the job anchor.
	fence, err := storelock.BeginFence(lease)
	if err != nil {
		return err
	}
	err, clean := runDirectChecked(ctx, cmd)
	if clean {
		if clearErr := fence.Clear(); clearErr != nil {
			return errors.Join(err, fmt.Errorf("clear command fence: %w", clearErr))
		}
	}
	return err
}

// ServeSupervisor is unavailable on Windows, which uses native job ownership.
func ServeSupervisor() int { return 125 }
