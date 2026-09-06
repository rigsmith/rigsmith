//go:build linux || darwin

package durable

import (
	"os"
	"path/filepath"
)

func replaceFile(from, to string) error { return os.Rename(from, to) }
func syncDirectory(dir string) error {
	// Include newly created ancestors: flushing only queue.json does not persist
	// a new directory entry on Unix. The queue lives on a stable local filesystem.
	for {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = f.Sync()
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}
