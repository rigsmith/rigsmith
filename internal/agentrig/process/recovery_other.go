//go:build !linux && !darwin

package process

func currentScope() (recoveryScope, error)  { return recoveryScope{}, ErrRecoveryScope }
func verifyOwnership(commandEvidence) error { return ErrRecoveryScope }
