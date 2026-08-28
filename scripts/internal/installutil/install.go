// Package installutil provides the shared filesystem setup used by the source
// and development installers.
package installutil

import (
	"fmt"
	"os"
)

const elevatedEnv = "RIGSMITH_INSTALL_ELEVATED"

// PrepareDir creates dir and verifies that installers can write to it. On
// Windows, a protected destination is retried in a UAC-elevated child.
// relaunched is true when the child completed the installation.
func PrepareDir(dir string) (relaunched bool, err error) {
	if err := ensureWritable(dir); err == nil {
		return false, nil
	} else if !shouldElevate(err) {
		return false, err
	} else if os.Getenv(elevatedEnv) != "" {
		return false, fmt.Errorf("%s is not writable after elevation: %w", dir, err)
	}

	fmt.Fprintf(os.Stderr, "Administrator access is required to write to %s; requesting permission...\n", dir)
	if err := relaunchElevated(); err != nil {
		return false, err
	}
	return true, nil
}

func ensureWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".rigsmith-write-test-*")
	if err != nil {
		return fmt.Errorf("checking write access to %s: %w", dir, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("checking write access to %s: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("cleaning up write check in %s: %w", dir, err)
	}
	return nil
}
