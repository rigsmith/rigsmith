//go:build !linux && !darwin && !windows

package process

func currentScope() (recoveryScope, error)  { return recoveryScope{}, ErrRecoveryScope }
func verifyOwnership(commandEvidence) error { return ErrRecoveryScope }
