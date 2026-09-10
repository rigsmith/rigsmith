package artifact

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

// Capacity accounts for direct archive and interrupted-write files only. It
// never opens archive contents, follows links or traverses build workspaces and
// substores. Sizes are logical file bytes, not allocated blocks or free space.
type Capacity struct {
	Archives, InterruptedWrites, BuildWorkspaces, OtherEntries int
	StoredBytes, InterruptedWriteBytes                         int64
	MaxStoredBytes, RemainingStoredBytes                       int64
	LimitEnabled                                               bool
}

// Capacity returns a consistent inventory under artifact-store ownership.
// A missing store is an error, not a newly initialized empty store. Corrupt
// regular archives still consume capacity; this observation does not verify them.
func (s Store) Capacity(ctx context.Context) (Capacity, error) {
	release, err := s.maintenanceLock(ctx)
	if err != nil {
		return Capacity{}, err
	}
	defer release()
	c, _, err := s.inventory(ctx)
	return c, err
}

func (s Store) maintenanceLock(ctx context.Context) (func(), error) {
	if _, err := s.path(strings.Repeat("0", 64)); err != nil {
		return nil, err
	}
	st, err := os.Lstat(s.Dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrInvalid
	}
	_, release, err := storelock.Acquire(ctx, s.Dir, 0)
	return release, err
}

type interruptedWrite struct {
	name string
	info os.FileInfo
}

func (s Store) inventory(ctx context.Context) (Capacity, []interruptedWrite, error) {
	c := Capacity{MaxStoredBytes: s.MaxStoredBytes, LimitEnabled: s.MaxStoredBytes > 0}
	f, err := os.Open(s.Dir)
	if err != nil {
		return Capacity{}, nil, err
	}
	defer f.Close()
	var writes []interruptedWrite
	entries := 0
	for {
		if err := ctx.Err(); err != nil {
			return Capacity{}, nil, err
		}
		batch, err := f.ReadDir(128)
		if err != nil && err != io.EOF {
			return Capacity{}, nil, err
		}
		for _, entry := range batch {
			entries++
			if entries > 100000 {
				return Capacity{}, nil, fmt.Errorf("artifact directory exceeds inventory entry limit")
			}
			name := entry.Name()
			archive := strings.HasSuffix(name, ".capture")
			write := strings.HasPrefix(name, ".durable-") && len(name) > len(".durable-")
			if !archive && !write {
				if entry.IsDir() && strings.HasPrefix(name, ".capture-work-") {
					c.BuildWorkspaces++
				} else {
					c.OtherEntries++
				}
				continue
			}
			info, err := entry.Info() // lstat semantics: never charge a linked target
			if err != nil {
				return Capacity{}, nil, err
			}
			if !info.Mode().IsRegular() || info.Size() < 0 {
				return Capacity{}, nil, ErrInvalid
			}
			if archive {
				if info.Size() > math.MaxInt64-c.StoredBytes {
					return Capacity{}, nil, ErrInvalid
				}
				c.Archives++
				c.StoredBytes += info.Size()
			} else {
				if info.Size() > math.MaxInt64-c.InterruptedWriteBytes {
					return Capacity{}, nil, ErrInvalid
				}
				c.InterruptedWrites++
				c.InterruptedWriteBytes += info.Size()
				writes = append(writes, interruptedWrite{name, info})
			}
		}
		if err == io.EOF {
			break
		}
	}
	if c.LimitEnabled {
		c.RemainingStoredBytes = max(int64(0), c.MaxStoredBytes-c.StoredBytes)
	}
	return c, writes, nil
}

type CleanupResult struct {
	RemovedFiles int
	RemovedBytes int64
}

// CleanupInterruptedWrites removes only top-level .durable-* regular files,
// in the store's reserved disposable archive-write namespace. Names alone confer
// eligibility; cleanup cannot prove which process created a file. Callers must
// never store unrelated data there, including names such as .durable-user-data.
// A .capture suffix takes precedence and is always retained as an archive.
// It never removes sealed archives, build workspaces, substores, publication
// scratch or recovery intents. Build workspaces may still have external writers
// protected by staging ownership after their parent process dies.
//
// The complete inventory is validated before removing anything. A later error
// returns partial removal counts; retrying safely removes remaining write files.
// This is space reclamation, not a durable acknowledgement: a power interruption
// can make deleted temporary files reappear, and another cleanup may be needed.
// Stable private directories and cooperating writers are required, as for Build.
func (s Store) CleanupInterruptedWrites(ctx context.Context) (CleanupResult, error) {
	return s.cleanupInterruptedWrites(ctx, os.Remove)
}

func (s Store) cleanupInterruptedWrites(ctx context.Context, remove func(string) error) (CleanupResult, error) {
	result := CleanupResult{}
	release, err := s.maintenanceLock(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	_, writes, err := s.inventory(ctx)
	if err != nil {
		return result, err
	}
	for _, write := range writes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path := filepath.Join(s.Dir, write.name)
		st, err := os.Lstat(path)
		if err != nil {
			return result, err
		}
		if !st.Mode().IsRegular() || !os.SameFile(st, write.info) || st.Size() != write.info.Size() {
			return result, ErrInvalid
		}
		if err := remove(path); err != nil {
			return result, err
		}
		result.RemovedFiles++
		result.RemovedBytes += st.Size()
	}
	return result, nil
}
