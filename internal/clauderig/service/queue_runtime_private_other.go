//go:build !linux && !darwin

package service

import "os"

// Windows ACLs are a prerequisite of the private runtime parent; POSIX mode bits
// do not describe that access. Runtime creation uses the existing durable/store
// primitives and does not rewrite or claim to validate inherited ACLs.
func runtimePrivate(os.FileInfo) bool { return true }
