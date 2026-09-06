package queue

import (
	"os"

	"golang.org/x/sys/unix"
)

func syncData(f *os.File) error {
	if err := f.Sync(); err != nil {
		return err
	}
	_, err := unix.FcntlInt(f.Fd(), unix.F_FULLFSYNC, 0)
	return err
}
