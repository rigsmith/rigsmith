//go:build !linux && !darwin && !windows

package files

import "os"

func lockReplacement(*os.File) (bool, error)  { return false, ErrSource }
func syncReplacementDirectory(*os.Root) error { return ErrSource }
