//go:build linux || darwin

package process

import (
	"errors"
	"os/exec"
	"syscall"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func verifyOwnership(e commandEvidence) error {
	if e.Group == 0 {
		// No command was authorized before the owner died.
		return nil
	}
	// Unlike enumerating /proc, the kernel's group lookup cannot miss a helper
	// that forks while a userspace snapshot is being collected. Zombies may make
	// recovery temporarily conservative; only ESRCH establishes group absence.
	if err := syscall.Kill(-e.Group, 0); errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return errors.Join(ErrWritersActive, err)
	}
	return ErrWritersActive
}

// prepareRecoverable pins a harmless group leader before sealing its identity.
// The standard Unix true utility receives no input or inherited environment.
// Its waitable process reserves the PGID until actual writers are drained.
func prepareRecoverable(cmd *exec.Cmd, fence *storelock.Fence) (*ownership, error) {
	if cmd.SysProcAttr != nil || cmd.Cancel != nil {
		return nil, errors.New("command already has process ownership")
	}
	data, err := fence.RecoveryEvidence()
	if err != nil {
		return nil, err
	}
	evidence, err := parseEvidence(data)
	if err != nil {
		return nil, err
	}
	scope, err := currentScope()
	if err != nil || evidence.Scope != scope || evidence.Group != 0 {
		return nil, ErrRecoveryScope
	}
	anchor := exec.Command("/usr/bin/true")
	anchor.Env = []string{}
	anchor.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := anchor.Start(); err != nil {
		return nil, err
	}
	owner := &ownership{group: anchor.Process.Pid, anchor: anchor}
	evidence.Group = owner.group
	if err := fence.SetRecoveryEvidence(evidence.bytes()); err != nil {
		return nil, errors.Join(err, owner.close())
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: owner.group}
	return owner, nil
}
