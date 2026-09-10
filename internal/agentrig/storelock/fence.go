package storelock

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
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
	return beginFence(ctx, nil)
}

// BeginRecoverableFence records bounded platform evidence before any writer is
// created. Only the trusted process owner supplies evidence, never user input.
func BeginRecoverableFence(ctx context.Context, evidence []byte) (*Fence, error) {
	if len(evidence) == 0 || len(evidence) > recoveryLimit {
		return nil, errors.New("invalid recovery evidence size")
	}
	return beginFence(ctx, evidence)
}

func beginFence(ctx context.Context, evidence []byte) (*Fence, error) {
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
	if evidence != nil {
		token = recoveryRecord(token[len(fencePrefix):], evidence)
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
	if !info.Mode().IsRegular() || (info.Size() != int64(fenceSize) && info.Size() != int64(recoverySize)) {
		return nil, fmt.Errorf("%w: invalid command record", ErrFenced)
	}
	token := make([]byte, int(info.Size()))
	if _, err := file.ReadAt(token, 0); err != nil {
		return nil, err
	}
	if len(token) == recoverySize {
		if _, err := recoveryEvidence(token); err != nil {
			return nil, err
		}
		return token, nil
	}
	if !bytes.HasPrefix(token, []byte(fencePrefix)) {
		return nil, fmt.Errorf("%w: unknown command record", ErrFenced)
	}
	return token, nil
}

// Clear records verified cleanup, independently of operation cancellation. An
// old completion cannot clear a newer command's intent. A flush failure is
// returned even if truncation already took effect: all writers were verified
// stopped before Clear was called, so either disk state is safe for restart.
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

const recoveryPrefix = "agentrig-command-fence-v2:"
const recoveryLimit = 2048
const recoverySize = len(recoveryPrefix) + 32 + 4 + recoveryLimit + sha256.Size

func recoveryRecord(nonce, evidence []byte) []byte {
	data := make([]byte, recoverySize)
	n := copy(data, recoveryPrefix)
	n += copy(data[n:], nonce)
	binary.BigEndian.PutUint32(data[n:], uint32(len(evidence)))
	copy(data[n+4:], evidence)
	sum := sha256.Sum256(data[:len(data)-sha256.Size])
	copy(data[len(data)-sha256.Size:], sum[:])
	return data
}

func recoveryEvidence(data []byte) ([]byte, error) {
	if len(data) != recoverySize || !bytes.HasPrefix(data, []byte(recoveryPrefix)) {
		return nil, fmt.Errorf("%w: record has no recovery evidence", ErrFenced)
	}
	sum := sha256.Sum256(data[:len(data)-sha256.Size])
	if !bytes.Equal(sum[:], data[len(data)-sha256.Size:]) {
		return nil, fmt.Errorf("%w: damaged recovery record", ErrFenced)
	}
	offset := len(recoveryPrefix) + 32
	n := binary.BigEndian.Uint32(data[offset:])
	if n == 0 || n > recoveryLimit {
		return nil, fmt.Errorf("%w: invalid recovery evidence size", ErrFenced)
	}
	return bytes.Clone(data[offset+4 : offset+4+int(n)]), nil
}

// RecoveryEvidence returns a private copy of the currently owned evidence.
func (f *Fence) RecoveryEvidence() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return recoveryEvidence(f.token)
}

// SetRecoveryEvidence seals ownership before launching a writer. Failure must
// prevent launch. Fixed-size checksummed records reject torn transitions; stale
// completions cannot clear a later phase. The caller keeps the lease throughout.
func (f *Fence) SetRecoveryEvidence(evidence []byte) error {
	return f.setRecoveryEvidence(evidence, func(next []byte) error {
		if n, err := f.file.WriteAt(next, 0); err != nil {
			return err
		} else if n != len(next) {
			return io.ErrShortWrite
		}
		return f.file.Sync()
	})
}

// The persistence boundary permits fault tests without changing live file or
// lock ownership. A failure never advances the in-memory completion token.
func (f *Fence) setRecoveryEvidence(evidence []byte, persist func([]byte) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(evidence) == 0 || len(evidence) > recoveryLimit {
		return errors.New("invalid recovery evidence size")
	}
	if _, err := recoveryEvidence(f.token); err != nil {
		return err
	}
	current, err := readFence(f.file)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, f.token) {
		return fmt.Errorf("%w: command identity changed", ErrFenced)
	}
	next := recoveryRecord(f.token[len(recoveryPrefix):len(recoveryPrefix)+32], evidence)
	if err := persist(next); err != nil {
		return err
	}
	f.token = next
	return nil
}

// RecoverFence holds the existing lock inode while a trusted platform owner
// proves its writers stopped. It never creates/replaces lock files, runs Git,
// grants a normal lease, or accepts legacy/damaged records as cleanup evidence.
// The callback must be read-only and return nil only on positive OS evidence.
func RecoverFence(ctx context.Context, dir string, verify func([]byte) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if verify == nil {
		return false, errors.New("recovery requires an ownership verifier")
	}
	if held, _ := ctx.Value(contextKey{}).(*lease); held != nil {
		return false, errors.New("recovery requires an independent operation context")
	}
	path, err := existingLockPath(dir)
	if err != nil {
		return false, err
	}
	expected, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !expected.Mode().IsRegular() {
		return false, fmt.Errorf("%w: recovery lock is not a regular file", ErrFenced)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		return false, fmt.Errorf("%w: recovery lock identity changed", ErrFenced)
	}
	if got, err := tryLock(file); err != nil {
		return false, err
	} else if !got {
		return false, ErrBusy
	}
	return recoverLockedFence(ctx, file, verify)
}

// recoverLockedFence requires exclusive ownership of file for the entire call.
func recoverLockedFence(ctx context.Context, file *os.File, verify func([]byte) error) (bool, error) {
	if err := checkRecoveryPath(file); err != nil {
		return false, err
	}
	if err := checkFence(file); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrFenced) {
		return false, err
	}
	token, err := readFence(file)
	if err != nil {
		return false, err
	}
	evidence, err := recoveryEvidence(token)
	if err != nil {
		return false, err
	}
	if err := verify(evidence); err != nil {
		return false, errors.Join(ErrFenced, err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := checkRecoveryPath(file); err != nil {
		return false, err
	}
	if err := (&Fence{file: file, token: token}).Clear(); err != nil {
		return false, err
	}
	return true, nil
}

// Reject accidental links and observed lock replacement. As with acquisition,
// coordination assumes local participants do not maliciously rewrite lock paths.
func checkRecoveryPath(file *os.File) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	named, err := os.Lstat(file.Name())
	if err != nil {
		return err
	}
	if !named.Mode().IsRegular() || !os.SameFile(opened, named) {
		return fmt.Errorf("%w: recovery lock identity changed", ErrFenced)
	}
	return nil
}
