package artifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func maintenanceBuild(_ context.Context, tree string) error {
	path := filepath.Join(tree, "payload")
	if err := os.WriteFile(path, bytes.Repeat([]byte("fixture"), 1024), 0600); err != nil {
		return err
	}
	stamp := time.Unix(1600000000, 0)
	return os.Chtimes(path, stamp, stamp)
}

func TestStoreCapacityAdmissionAndRetainedReuse(t *testing.T) {
	s := testStore(t)
	key := Key([]byte("retained"))
	ref, err := s.Build(t.Context(), key, maintenanceBuild)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.Capacity(t.Context())
	if err != nil || usage.Archives != 1 || usage.StoredBytes <= 0 || usage.LimitEnabled {
		t.Fatal(usage, err)
	}
	path, _ := s.path(key)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.MaxStoredBytes = usage.StoredBytes + 1
	if _, err := s.Build(t.Context(), Key([]byte("no-room")), func(context.Context, string) error { t.Fatal("build started with no header space"); return nil }); !errors.Is(err, ErrStoreFull) {
		t.Fatal(err)
	}
	s.MaxStoredBytes = usage.StoredBytes + usage.StoredBytes/2
	if _, err := s.Build(t.Context(), Key([]byte("overflow")), maintenanceBuild); !errors.Is(err, ErrStoreFull) {
		t.Fatal("wrong aggregate error", err)
	}
	if entries, err := os.ReadDir(s.Dir); err != nil || len(entries) != 1 {
		t.Fatal("published partial archive", entries, err)
	}
	s.MaxStoredBytes = 1 // configuration changes must not prevent recovery of saved output
	if got, err := s.Build(t.Context(), key, func(context.Context, string) error { t.Fatal("recaptured retained output"); return nil }); err != nil || got != ref {
		t.Fatal(got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("retained bytes changed", err)
	}
	usage, err = s.Capacity(t.Context())
	if err != nil || usage.RemainingStoredBytes != 0 || !usage.LimitEnabled {
		t.Fatal(usage, err)
	}
	s.MaxStoredBytes = usage.StoredBytes * 3
	// Repair capacity and retry the same failed identity, without rebinding work.
	if _, err := s.Build(t.Context(), Key([]byte("overflow")), maintenanceBuild); err != nil {
		t.Fatal(err)
	}
	usage, err = s.Capacity(t.Context())
	if err != nil || usage.Archives != 2 || usage.StoredBytes > s.MaxStoredBytes {
		t.Fatal(usage, err)
	}
	s.MaxStoredBytes = 0
	s.MaxBytes = 1024
	if _, err := s.Build(t.Context(), Key([]byte("individual")), maintenanceBuild); !errors.Is(err, ErrTooLarge) {
		t.Fatal("lost individual limit", err)
	}
	s.MaxBytes = 0
	s.MaxStoredBytes = -1
	if _, err := s.Build(t.Context(), Key([]byte("invalid")), maintenanceBuild); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestStoreCapacitySerializesIndependentBuilders(t *testing.T) {
	sample := testStore(t)
	if _, err := sample.Build(t.Context(), Key([]byte("sample")), maintenanceBuild); err != nil {
		t.Fatal(err)
	}
	u, err := sample.Capacity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	s.MaxStoredBytes = u.StoredBytes
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"one", "two"} {
		wg.Go(func() { <-start; _, err := s.Build(t.Context(), Key([]byte(key)), maintenanceBuild); results <- err })
	}
	close(start)
	wg.Wait()
	close(results)
	success, full := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrStoreFull) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || full != 1 {
		t.Fatal(success, full)
	}
	u, err = s.Capacity(t.Context())
	if err != nil || u.Archives != 1 || u.StoredBytes > s.MaxStoredBytes {
		t.Fatal(u, err)
	}
}

func TestInterruptedWriteCleanupPreservesAllOtherState(t *testing.T) {
	s := testStore(t)
	key := Key([]byte("keep"))
	ref, err := s.Build(t.Context(), key, maintenanceBuild)
	if err != nil {
		t.Fatal(err)
	}
	// Build work, seed/recovery substores and publication scratch may have live
	// dependencies or external writers. None belongs to this deletion namespace.
	for _, dir := range []string{".capture-work-live", "seeds", "merges", ".publication-live"} {
		if err := os.Mkdir(filepath.Join(s.Dir, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Dir, dir, ".durable-nested"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"unknown", ".durable-", "damaged.capture", ".durable-retained.capture"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Reserved names are disposable regardless of their suffix or contents.
	for _, name := range []string{".durable-one", ".durable-user-data"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("discard"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Capacity(t.Context())
	if err != nil || u.Archives != 3 || u.InterruptedWrites != 2 || u.InterruptedWriteBytes != 14 || u.BuildWorkspaces != 1 || u.OtherEntries != 5 {
		t.Fatal(u, err)
	}
	result, err := s.CleanupInterruptedWrites(t.Context())
	if err != nil || result.RemovedFiles != 2 || result.RemovedBytes != 14 {
		t.Fatal(result, err)
	}
	if err := s.Verify(t.Context(), ref); err != nil {
		t.Fatal("lost sealed archive", err)
	}
	for _, name := range []string{"unknown", ".durable-", "damaged.capture", ".durable-retained.capture", ".capture-work-live/.durable-nested", "seeds/.durable-nested", "merges/.durable-nested", ".publication-live/.durable-nested"} {
		b, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(name)))
		if err != nil || string(b) != "keep" {
			t.Fatal("changed protected state", name, err)
		}
	}
	if result, err := s.CleanupInterruptedWrites(t.Context()); err != nil || result.RemovedFiles != 0 {
		t.Fatal("retry not idempotent", result, err)
	}
}

func TestInterruptedWriteCleanupFailureCancellationAndFence(t *testing.T) {
	s := testStore(t)
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".durable-one", ".durable-two"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := s.CleanupInterruptedWrites(ctx); !errors.Is(err, context.Canceled) || result.RemovedFiles != 0 {
		t.Fatal(result, err)
	}
	locked, release, err := storelock.Acquire(t.Context(), s.Dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capacity(t.Context()); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal(err)
	}
	if _, err := s.CleanupInterruptedWrites(t.Context()); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal(err)
	}
	fence, err := storelock.BeginFence(locked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupInterruptedWrites(locked); !errors.Is(err, storelock.ErrFenced) {
		t.Fatal(err)
	}
	if err := fence.Clear(); err != nil {
		t.Fatal(err)
	}
	release()
	calls := 0
	result, err := s.cleanupInterruptedWrites(t.Context(), func(path string) error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		}
		return os.Remove(path)
	})
	if !errors.Is(err, os.ErrPermission) || result.RemovedFiles != 1 || result.RemovedBytes != 7 {
		t.Fatal("partial error lost progress", result, err)
	}
	if result, err := s.CleanupInterruptedWrites(t.Context()); err != nil || result.RemovedFiles != 1 {
		t.Fatal(result, err)
	}
	missing := testStore(t)
	if _, err := missing.CleanupInterruptedWrites(t.Context()); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing.Dir); !os.IsNotExist(err) {
		t.Fatal("initialized missing store", err)
	}
}

func TestInterruptedWriteCleanupRejectsNonregularCandidates(t *testing.T) {
	for _, mode := range []string{"directory", "linked-write", "linked-archive"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if err := os.Mkdir(s.Dir, 0700); err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(s.Dir, ".durable-regular")
			if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			name := ".durable-bad"
			if mode == "linked-archive" {
				name = "bad.capture"
			}
			if mode == "directory" {
				if err := os.Mkdir(filepath.Join(s.Dir, name), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(s.Dir, name)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if result, err := s.CleanupInterruptedWrites(t.Context()); !errors.Is(err, ErrInvalid) || result.RemovedFiles != 0 {
				t.Fatal(result, err)
			}
			if b, err := os.ReadFile(keep); err != nil || string(b) != "keep" {
				t.Fatal("removed files before validating inventory", err)
			}
			if _, err := s.Capacity(t.Context()); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestCleanupStopsBetweenRemovalsOnCancellation(t *testing.T) {
	s := testStore(t)
	if err := os.Mkdir(s.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".durable-one", ".durable-two"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	r, err := s.cleanupInterruptedWrites(ctx, func(path string) error { err := os.Remove(path); cancel(); return err })
	if !errors.Is(err, context.Canceled) || r.RemovedFiles != 1 {
		t.Fatal(r, err)
	}
}
