//go:build darwin || linux

package files

import (
	"golang.org/x/sys/unix"
	"os"
)

func openSourceFile(root *os.Root, name string) (*os.File, error) {
	// Nonblocking prevents a regular-file-to-FIFO swap from waiting for a writer.
	// No-follow prevents opening a last-component symlink even within the root.
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	// Root.OpenFile follows internal links even with O_NOFOLLOW. Use openat on
	// the pinned directory for this validated direct-child name instead.
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "source"), nil
}
