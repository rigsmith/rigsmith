//go:build linux || darwin

package storelock

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Inherit duplicates an active lease for transfer to a command supervisor via
// exec.Cmd.ExtraFiles. The caller must close the duplicate after spawning (or on
// failure). The supervisor must retain its copy until all writers have stopped.
// This shares the flock's open file description; do not explicitly unlock it.
// The duplicate is close-on-exec until explicitly inherited by the supervisor.
func Inherit(ctx context.Context) (*os.File, error) {
	held, _ := ctx.Value(contextKey{}).(*lease)
	if held == nil {
		return nil, errors.New("command supervision requires a staging lease")
	}
	held.mu.Lock()
	defer held.mu.Unlock()
	if held.refs <= 0 {
		return nil, errors.New("command supervision requires an active staging lease")
	}
	fd, err := unix.FcntlInt(held.file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), held.file.Name()), nil
}
