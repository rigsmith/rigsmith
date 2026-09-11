package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrReplacementBusy      = errors.New("another replacement writer holds the destination")
	ErrReplacementState     = errors.New("invalid or consumed file replacement")
	ErrReplacementCleanup   = errors.New("private replacement scratch cleanup failed")
	ErrReplacementUncertain = errors.New("file replacement may have completed; inspect the destination before retrying")
)

const replacementLock = ".agentrig-replace.lock"
const replacementPrefix = ".agentrig-replace-"

// ExpectedFile is a caller's observed content state. Absent and empty are distinct.
// Stage also captures file identity/mode/mtime for checks before replacement.
type ExpectedFile struct {
	Exists bool
	SHA256 [32]byte
}

type replacement struct {
	name, temp       string
	original, staged os.FileInfo
	expected         ExpectedFile
	digest           [32]byte
	limit            int64
	attempted        bool
	ready            bool
}

// Replacements stages and installs complete direct-child files in a pinned root.
// Every participating writer must use the same lock. Other applications do not
// participate; callers must coordinate those writers separately. Methods are
// sequential. Close before closing Source. Scratch files are created with 0600
// (Windows inherits directory ACLs); crash leftovers are never auto-deleted.
type Replacements struct {
	source   *Source
	lock     *os.File
	lockInfo os.FileInfo
	entries  map[string]*replacement
	closed   bool
}

// BeginReplace acquires a nonblocking OS lock on a fixed regular direct child.
// The empty lock file remains in place after close; deleting it would split locks.
func BeginReplace(ctx context.Context, source *Source) (*Replacements, error) {
	if source == nil {
		return nil, ErrSource
	}
	if err := source.Check(ctx); err != nil {
		return nil, err
	}
	f, err := source.root.OpenFile(replacementLock, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrExist) {
		f, err = openSourceFile(source.root, replacementLock)
	}
	if err != nil {
		return nil, sourceError(err)
	}
	owned := false
	defer func() {
		if !owned {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		return nil, ErrSource
	}
	locked, err := lockReplacement(f)
	if err != nil {
		return nil, ErrSource
	}
	if !locked {
		return nil, ErrReplacementBusy
	}
	batch := &Replacements{source: source, lock: f, lockInfo: info, entries: map[string]*replacement{}}
	if err := batch.check(ctx); err != nil {
		return nil, err
	}
	owned = true
	return batch, nil
}

func (b *Replacements) check(ctx context.Context) error {
	if b.closed || b.source == nil || b.lock == nil {
		return ErrReplacementState
	}
	if err := b.source.Check(ctx); err != nil {
		return err
	}
	named, err := b.source.root.Lstat(replacementLock)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(named, b.lockInfo) {
		return ErrSourceChanged
	}
	return nil
}

func replacementName(name string) bool {
	return filepath.IsLocal(name) && name != "." && !strings.ContainsAny(name, "/\\:") && !IsReplacementArtifact(name)
}

// IsReplacementArtifact identifies reserved direct-child names, not ownership
// or safe-to-delete files. Callers must still bound their total enumeration.
func IsReplacementArtifact(name string) bool {
	return strings.EqualFold(name, replacementLock) || strings.HasPrefix(strings.ToLower(name), replacementPrefix)
}

// Stage verifies expected content, then writes, flushes and closes a temporary
// sibling. It does not modify the destination. Duplicate/reserved names fail.
func (b *Replacements) Stage(ctx context.Context, name string, expected ExpectedFile, data []byte, limit int64) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	if !replacementName(name) || b.entries[name] != nil {
		return ErrReplacementState
	}
	if limit <= 0 || int64(len(data)) > limit {
		return ErrSourceLimit
	}
	r := &replacement{name: name, expected: expected, digest: sha256.Sum256(data), limit: limit}
	info, err := b.inspect(ctx, r)
	if err != nil {
		return err
	}
	r.original = info
	r.temp = replacementPrefix + rand.Text()
	f, err := b.source.root.OpenFile(r.temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sourceError(err)
	}
	b.entries[name] = r // Close owns scratch cleanup even after a staging failure.
	defer f.Close()
	r.staged, err = f.Stat()
	if err != nil {
		return ErrReplacementCleanup
	}
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(data), 32<<10)
		written, err := f.Write(data[:n])
		if err != nil || written != n {
			return ErrSource
		}
		data = data[n:]
	}
	if err := f.Sync(); err != nil {
		return ErrSource
	}
	r.staged, err = f.Stat()
	if err != nil {
		return ErrSource
	}
	if err := f.Close(); err != nil {
		return ErrSource
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	r.ready = true
	return nil
}

func (b *Replacements) inspect(ctx context.Context, r *replacement) (os.FileInfo, error) {
	before, err := b.source.root.Lstat(r.name)
	if !r.expected.Exists {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, sourceError(err)
		}
		return nil, ErrSourceChanged
	}
	if err != nil {
		return nil, sourceError(err)
	}
	if !before.Mode().IsRegular() {
		return nil, ErrSource
	}
	if r.original != nil && !sameSource(r.original, before) {
		return nil, ErrSourceChanged
	}
	data, err := b.source.Read(ctx, r.name, r.limit)
	if err != nil {
		return nil, err
	}
	after, err := b.source.root.Lstat(r.name)
	if err != nil {
		return nil, sourceError(err)
	}
	if !sameSource(before, after) || sha256.Sum256(data) != r.expected.SHA256 {
		return nil, ErrSourceChanged
	}
	return before, nil
}

// Apply consumes one staged entry. New files use no-replace hard-link creation;
// existing files use root-relative rename, never truncate/remove/copy fallback.
// Once an installation syscall is attempted, ambiguous errors are reported as
// ErrReplacementUncertain. Success confirms observed complete bytes, not a
// multi-file transaction or a universal power-loss guarantee.
func (b *Replacements) Apply(ctx context.Context, name string) error {
	if err := b.check(ctx); err != nil {
		return err
	}
	r := b.entries[name]
	if r == nil || !r.ready || r.attempted {
		return ErrReplacementState
	}
	r.attempted = true
	if _, err := b.inspect(ctx, r); err != nil {
		return err
	}
	before, err := b.source.root.Lstat(r.temp)
	if err != nil || r.staged == nil || !sameSource(r.staged, before) {
		return ErrSourceChanged
	}
	data, err := b.source.Read(ctx, r.temp, r.limit)
	if err != nil {
		return err
	}
	if sha256.Sum256(data) != r.digest {
		return ErrSourceChanged
	}
	if err := b.check(ctx); err != nil {
		return err
	}
	// Recheck the target after reading scratch, just before the installation.
	if _, err := b.inspect(ctx, r); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.expected.Exists {
		err = b.source.root.Rename(r.temp, r.name)
	} else {
		err = b.source.root.Link(r.temp, r.name)
		if errors.Is(err, os.ErrExist) {
			return ErrSourceChanged
		}
	}
	if err != nil {
		return ErrReplacementUncertain
	}
	if !r.expected.Exists {
		if err := b.removeScratch(r); err != nil {
			return ErrReplacementUncertain
		}
	}
	if err := syncReplacementDirectory(b.source.root); err != nil {
		return ErrReplacementUncertain
	}
	installed, err := b.source.root.Lstat(r.name)
	if err != nil || !sameSource(r.staged, installed) {
		return ErrReplacementUncertain
	}
	data, err = b.source.Read(ctx, r.name, r.limit)
	if err != nil || sha256.Sum256(data) != r.digest {
		return ErrReplacementUncertain
	}
	if err := b.check(ctx); err != nil {
		return ErrReplacementUncertain
	}
	return nil
}

func (b *Replacements) removeScratch(r *replacement) error {
	if r.temp == "" {
		return nil
	}
	info, err := b.source.root.Lstat(r.temp)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || r.staged == nil || !info.Mode().IsRegular() || !os.SameFile(info, r.staged) {
		return ErrReplacementCleanup
	}
	if err := b.source.root.Remove(r.temp); err != nil {
		return ErrReplacementCleanup
	}
	return nil
}

// Close removes only scratch entries still naming our original regular objects,
// then releases the OS lock. It never deletes the fixed lock file or targets.
func (b *Replacements) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	var result error
	for _, r := range b.entries {
		if err := b.removeScratch(r); err != nil {
			result = ErrReplacementCleanup
		}
	}
	if err := b.lock.Close(); err != nil {
		result = ErrReplacementCleanup
	}
	b.entries = nil
	return result
}
