// Package durable publishes complete files using platform flush and replacement
// primitives. Callers provide serialization and hold their operation lock.
package durable

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrUncertain = errors.New("write may have committed; retry the same operation with the same identity and arguments")

// Write writes a private temporary sibling, flushes and closes it, replaces the
// target, then flushes directory entries on Unix. The parent must already exist.
// The callback must not close f. Failure before replacement retains the target;
// replacement/directory flush failures return ErrUncertain. No remove/copy fallback
// or truncation of the target is used. Stable local filesystems only.
func Write(ctx context.Context, path string, write func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".durable-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = write(f); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = syncData(f); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceFile(f.Name(), path); err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	return nil
}

// Rewrite reflushes an existing regular file before acknowledging an earlier
// uncertain write. Merely reading the file does not confirm its durability.
func Rewrite(ctx context.Context, path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("durable source must be regular")
	}
	return Write(ctx, path, func(out *os.File) error {
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
