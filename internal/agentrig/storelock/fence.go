package storelock

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// ErrFenced means a previous command has not proved that all its writers stopped.
// A released OS lock, elapsed time, or a new worker is not cleanup evidence.
var ErrFenced = errors.New("staging store is fenced: previous command cleanup is unconfirmed")

const fencePrefix = "agentrig-command-fence-v1:"
const fenceSize = len(fencePrefix) + 32

// Fence records command intent in the existing lock inode, outside Git. Never
// replace or unlink that inode. The caller must retain its lease while using a
// Fence; Clear is permitted only after every possible writer has stopped.
// No automatic timeout, PID-based reset, or operator bypass is provided.
type Fence struct {
	mu    sync.Mutex
	file  *os.File
	token []byte
}

func checkFence(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != 0 {
		return ErrFenced
	}
	return nil
}

// BeginFence synchronously records intent before creating any external writer.
// A failed/partial write is left in place and also blocks future acquisition.
// The caller must retain the context's lease through cleanup and Clear.
func BeginFence(ctx context.Context) (*Fence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	held, _ := ctx.Value(contextKey{}).(*lease)
	if held == nil {
		return nil, errors.New("command supervision requires a staging lease")
	}
	held.mu.Lock()
	defer held.mu.Unlock()
	if held.refs <= 0 {
		return nil, errors.New("command supervision requires an active staging lease")
	}
	if err := checkFence(held.file); err != nil {
		return nil, err
	}
	// A fixed-size random token keeps validation bounded.
	token := make([]byte, fenceSize)
	copy(token, fencePrefix)
	if _, err := rand.Read(token[len(fencePrefix):]); err != nil {
		return nil, err
	}
	if n, err := held.file.WriteAt(token, 0); err != nil {
		return nil, err
	} else if n != len(token) {
		return nil, io.ErrShortWrite
	}
	if err := held.file.Sync(); err != nil {
		return nil, err
	}
	return &Fence{file: held.file, token: token}, nil
}

// InheritedFence adopts existing intent through an explicitly inherited lease
// descriptor. Only a dedicated command supervisor may call this: ownership of
// this open file description must last through command cleanup. It creates no
// new intent and cannot turn a missing or damaged record into a clean store.
func InheritedFence(file *os.File) (*Fence, error) {
	token, err := readFence(file)
	if err != nil {
		return nil, err
	}
	return &Fence{file: file, token: token}, nil
}

func readFence(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(fenceSize) {
		return nil, fmt.Errorf("%w: invalid command record", ErrFenced)
	}
	token := make([]byte, fenceSize)
	if _, err := file.ReadAt(token, 0); err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(token, []byte(fencePrefix)) {
		return nil, fmt.Errorf("%w: unknown command record", ErrFenced)
	}
	return token, nil
}

// Clear records verified cleanup, independently of operation cancellation. An
// old completion cannot clear a newer command's intent. Failure retains or
// conservatively reports the fence; it must never be hidden by a command exit.
func (f *Fence) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	token, err := readFence(f.file)
	if err != nil {
		return err
	}
	if !bytes.Equal(token, f.token) {
		return fmt.Errorf("%w: command identity changed", ErrFenced)
	}
	if err := f.file.Truncate(0); err != nil {
		return err
	}
	return f.file.Sync()
}
