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
	evidence, err := newEvidence()
	if err != nil {
		return err
	}
	// Intent precedes preparation; the anchor never resumes and cannot write.
	fence, err := storelock.BeginRecoverableFence(lease, evidence.bytes())
	if err != nil {
		return err
	}
	owner, err := prepare(cmd)
	if err != nil {
		return errors.Join(err, fence.Clear())
	}
	evidence.State = "owned"
	if err := fence.SetRecoveryEvidence(evidence.bytes()); err != nil {
		// No Git has started. Preserve damaged/uncertain transitions and retire
		// the harmless suspended anchor; never fall back to unfenced execution.
		return errors.Join(err, owner.close())
	}
	err, cleanupVerified := runOwnedChecked(ctx, cmd, owner)
	if cleanupVerified {
		if sealErr := sealWindowsCleanup(fence, evidence); sealErr != nil {
			return errors.Join(err, fmt.Errorf("record command cleanup: %w", sealErr))
		}
		if clearErr := fence.Clear(); clearErr != nil {
			return errors.Join(err, fmt.Errorf("clear command fence: %w", clearErr))
		}
	}
	return err
}

// Called only after native ownership reports every writer stopped. Persisting
// that proof makes a crash before Clear recoverable without another OS restart.
func sealWindowsCleanup(fence *storelock.Fence, evidence commandEvidence) error {
	evidence.State = "stopped"
	return fence.SetRecoveryEvidence(evidence.bytes())
}

// ServeSupervisor is unavailable on Windows, which uses native job ownership.
func ServeSupervisor() int { return 125 }
