package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func cleanupFixture(t *testing.T) WorkspaceCleanup {
	t.Helper()
	root := t.TempDir()
	req := WorkspaceCleanup{StagingDir: filepath.Join(root, "stage"), Captures: artifact.Store{Dir: filepath.Join(root, "captures")}, Commits: artifact.Store{Dir: filepath.Join(root, "commits")}}
	for _, dir := range []string{req.StagingDir, req.Captures.Dir, SeedStore(req.Captures).Dir, req.Commits.Dir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return req
}
func cleanupPut(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("retained fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCleanupWorkspacesPreservesRetainedState(t *testing.T) {
	req := cleanupFixture(t)
	var removed, kept []string
	for _, root := range []string{req.Captures.Dir, SeedStore(req.Captures).Dir, req.Commits.Dir} {
		removed = append(removed, cleanupPut(t, root, ".capture-work-old/git/objects/fixture"))
		for _, name := range []string{"sealed.capture", ".durable-partial", "unknown/file", ".capture-work-/file", "merges/intent/plan", "repairs/sealed.capture"} {
			kept = append(kept, cleanupPut(t, root, name))
		}
	}
	for _, prefix := range []string{".publication-", ".startup-history-", ".confirmation-"} {
		removed = append(removed, cleanupPut(t, req.Commits.Dir, prefix+"old/git/config"))
		kept = append(kept, cleanupPut(t, req.Captures.Dir, prefix+"foreign/keep"))
	}
	kept = append(kept, cleanupPut(t, req.StagingDir, ".publication-native/keep"))
	// Read-only Git object files must not make native Windows reclamation fail.
	if err := os.Chmod(removed[0], 0400); err != nil {
		t.Fatal(err)
	}
	result, err := CleanupWorkspaces(t.Context(), req)
	if err != nil || result.RemovedWorkspaces != 6 {
		t.Fatal(result, err)
	}
	for _, p := range removed {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatal("scratch retained", p, err)
		}
	}
	for _, p := range kept {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "retained fixture" {
			t.Fatal("retained state changed", p, err)
		}
	}
	result, err = CleanupWorkspaces(t.Context(), req)
	if err != nil || result.RemovedWorkspaces != 0 {
		t.Fatal(result, err)
	}
}

func TestCleanupWorkspacesOwnershipAndWholeInventory(t *testing.T) {
	for _, mode := range []string{"stage-busy", "capture-busy", "seed-busy", "commit-busy", "stage-fenced", "seed-fenced", "borrowed", "canceled", "invalid-last-store", "linked-candidate", "linked-seeds"} {
		t.Run(mode, func(t *testing.T) {
			req := cleanupFixture(t)
			keep := cleanupPut(t, req.Captures.Dir, ".capture-work-first/file")
			ctx := t.Context()
			var want error
			switch mode {
			case "stage-busy", "capture-busy", "seed-busy", "commit-busy", "stage-fenced", "seed-fenced", "borrowed":
				dir := req.StagingDir
				if mode == "capture-busy" {
					dir = req.Captures.Dir
				}
				if mode == "seed-busy" || mode == "seed-fenced" {
					dir = SeedStore(req.Captures).Dir
				}
				if mode == "commit-busy" {
					dir = req.Commits.Dir
				}
				held, release, err := storelock.Acquire(ctx, dir, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				if mode == "stage-fenced" || mode == "seed-fenced" {
					if _, err := storelock.BeginFence(held); err != nil {
						t.Fatal(err)
					}
					release()
				} else if mode == "borrowed" {
					ctx = held
				} else {
					want = storelock.ErrBusy
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
				want = context.Canceled
			case "invalid-last-store":
				cleanupPut(t, req.Commits.Dir, ".publication-not-directory")
			case "linked-candidate":
				if err := os.Symlink(t.TempDir(), filepath.Join(req.Commits.Dir, ".publication-linked")); err != nil {
					t.Skip(err)
				}
			case "linked-seeds":
				dir := SeedStore(req.Captures).Dir
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Skip(err)
				}
			}
			result, err := CleanupWorkspaces(ctx, req)
			if err == nil || result.RemovedWorkspaces != 0 || (want != nil && !errors.Is(err, want)) {
				t.Fatal(result, err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatal("deleted before validating all stores", err)
			}
		})
	}
}

func TestCleanupWorkspacesPartialRemovalAndRetry(t *testing.T) {
	for _, cancelMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancelMode], func(t *testing.T) {
			req := cleanupFixture(t)
			cleanupPut(t, req.Captures.Dir, ".capture-work-one/file")
			cleanupPut(t, req.Commits.Dir, ".publication-two/file")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			result, err := cleanupWorkspaces(ctx, req, func(root *os.Root, name string) error {
				calls++
				if calls == 2 {
					return os.ErrPermission
				}
				if err := root.RemoveAll(name); err != nil {
					return err
				}
				if cancelMode {
					cancel()
				}
				return nil
			})
			want := error(os.ErrPermission)
			if cancelMode {
				want = context.Canceled
			}
			if result.RemovedWorkspaces != 1 || !errors.Is(err, want) {
				t.Fatal(result, err)
			}
			result, err = CleanupWorkspaces(t.Context(), req)
			if err != nil || result.RemovedWorkspaces != 1 {
				t.Fatal(result, err)
			}
		})
	}
}

func TestCleanupWorkspacesPathsAndLinks(t *testing.T) {
	t.Run("nested-link", func(t *testing.T) {
		req := cleanupFixture(t)
		outside := t.TempDir()
		keep := cleanupPut(t, outside, "keep")
		cleanupPut(t, req.Captures.Dir, ".capture-work-old/file")
		if err := os.Symlink(outside, filepath.Join(req.Captures.Dir, ".capture-work-old", "link")); err != nil {
			t.Skip(err)
		}
		result, err := CleanupWorkspaces(t.Context(), req)
		if err != nil || result.RemovedWorkspaces != 1 {
			t.Fatal(result, err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatal("followed nested link", err)
		}
	})
	t.Run("root-alias", func(t *testing.T) {
		req := cleanupFixture(t)
		cleanupPut(t, req.Captures.Dir, ".capture-work-old/file")
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(req.Captures.Dir, alias); err != nil {
			t.Skip(err)
		}
		req.Captures.Dir = alias
		result, err := CleanupWorkspaces(t.Context(), req)
		if err != nil || result.RemovedWorkspaces != 1 {
			t.Fatal(result, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		req := cleanupFixture(t)
		os.RemoveAll(req.Captures.Dir)
		os.RemoveAll(req.Commits.Dir)
		result, err := CleanupWorkspaces(t.Context(), req)
		if err != nil || result.RemovedWorkspaces != 0 {
			t.Fatal(result, err)
		}
		for _, dir := range []string{req.Captures.Dir, req.Commits.Dir} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("initialized store", err)
			}
		}
		os.Remove(req.StagingDir)
		if _, err := CleanupWorkspaces(t.Context(), req); !os.IsNotExist(err) {
			t.Fatal(err)
		}
	})
	t.Run("overlap", func(t *testing.T) {
		req := cleanupFixture(t)
		keep := cleanupPut(t, req.Captures.Dir, ".capture-work-old/file")
		req.Commits.Dir = filepath.Join(req.Captures.Dir, "nested")
		if _, err := CleanupWorkspaces(t.Context(), req); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCleanupWorkspacesKeepsLeasesAndRejectsReplacement(t *testing.T) {
	req := cleanupFixture(t)
	cleanupPut(t, req.Captures.Dir, ".capture-work-first/file")
	later := filepath.Join(req.Commits.Dir, ".publication-later")
	cleanupPut(t, later, "file")
	result, err := cleanupWorkspaces(t.Context(), req, func(root *os.Root, name string) error {
		for _, dir := range []string{req.StagingDir, req.Captures.Dir, SeedStore(req.Captures).Dir, req.Commits.Dir} {
			_, release, err := storelock.Acquire(t.Context(), dir, 0)
			if err == nil {
				release()
				t.Fatal("released writer ownership before deletion", dir)
			}
			if !errors.Is(err, storelock.ErrBusy) {
				t.Fatal(err)
			}
		}
		if err := root.RemoveAll(name); err != nil {
			return err
		}
		// Simulate violation of the stable-directory contract between candidates.
		// Keep the old inode alive to avoid allocator reuse in the fixture.
		if err := os.Rename(later, later+"-moved"); err != nil {
			return err
		}
		cleanupPut(t, later, "replacement")
		return nil
	})
	if result.RemovedWorkspaces != 1 || !errors.Is(err, ErrInvalid) {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(filepath.Join(later, "replacement")); err != nil {
		t.Fatal("deleted replacement", err)
	}
}

func TestCleanupWorkspacesValidatesUnderAllLeases(t *testing.T) {
	req := cleanupFixture(t)
	keep := cleanupPut(t, req.Captures.Dir, ".capture-work-old/file")
	refused := errors.New("policy changed")
	called := false
	req.Validate = func(ctx context.Context) error {
		called = true
		nested, release, err := storelock.Acquire(ctx, req.StagingDir, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if !storelock.SameActiveLease(ctx, nested) {
			t.Fatal("validator lacks staging ownership")
		}
		for _, dir := range []string{req.StagingDir, req.Captures.Dir, SeedStore(req.Captures).Dir, req.Commits.Dir} {
			_, release, err := storelock.Acquire(t.Context(), dir, 0)
			if err == nil {
				release()
				t.Fatal("validator ran without writer ownership", dir)
			}
			if !errors.Is(err, storelock.ErrBusy) {
				t.Fatal(err)
			}
		}
		return refused
	}
	result, err := CleanupWorkspaces(t.Context(), req)
	if !called || !errors.Is(err, refused) || result.RemovedWorkspaces != 0 {
		t.Fatal(called, result, err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("removed before validation", err)
	}
}

func TestCleanupWorkspaceRootRejectsReplacement(t *testing.T) {
	for _, mode := range []string{"leaf", "ancestor"} {
		t.Run(mode, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "parent")
			dir := filepath.Join(parent, "captures")
			cleanupPut(t, dir, ".capture-work-old/file")
			original, err := directoryIdentity(os.Open(dir))
			if err != nil {
				t.Fatal(err)
			}
			replacement := dir
			if mode == "ancestor" {
				replacement = parent
			}
			if err := os.Rename(replacement, replacement+"-moved"); err != nil {
				t.Fatal(err)
			}
			keep := cleanupPut(t, dir, ".capture-work-unrelated/file")
			root, err := openWorkspaceRoot(dir, original)
			if root != nil {
				root.Close()
				t.Fatal("opened replacement")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatal("changed replacement bytes", err)
			}
		})
	}
}
