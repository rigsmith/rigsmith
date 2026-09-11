//go:build !darwin && !linux && !windows

package files

import "os"

func openSourceFile(_ *os.Root, _ string) (*os.File, error) {
	return nil, ErrSource // No weaker fallback on unsupported platforms.
}
