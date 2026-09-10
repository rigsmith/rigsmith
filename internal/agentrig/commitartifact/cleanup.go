package commitartifact

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

// WorkspaceCleanup names private stores used exclusively with one staging
// directory. Every external writer must hold that staging lease through verified
// child cleanup (including supervision/fencing after owner death). All archive
// builders must hold their artifact-store lease. The directories and ancestors
// must remain stable and outside native sources/backups; adapters validate those
// vendor-specific roots. Uncoordinated/older writers are not supported.
//
// Direct .capture-work-* directories in captures, captures/seeds and commits,
// and .publication-*, .startup-history-* and .confirmation-* in commits, are
// reserved disposable namespaces. Never put unrelated data there. Sealed
// artifacts, recovery stores, relocated OS-temp scratch and unknown entries are
// outside this operation. Missing artifact stores are skipped, never created.
type WorkspaceCleanup struct {
	StagingDir        string
	Captures, Commits artifact.Store
	// Validate optionally rechecks adapter policy after every writer lease is
	// acquired, before inventory or deletion. It must be read-only and must not
	// retain the supplied staging-lease context beyond the call.
	Validate func(context.Context) error
}

// WorkspaceCleanupResult counts fully removed top-level workspaces in this
// attempt, not recursive files/bytes. A failed removal may partially empty its
// workspace without incrementing the count. Retry removes remaining scratch.
type WorkspaceCleanupResult struct{ RemovedWorkspaces int }

// CleanupWorkspaces explicitly reclaims abandoned disposable workspaces. It
// acquires staging, capture, seed and commit ownership without waiting, validates
// all candidates before deleting any, then holds every lease through removal.
// Busy owners and restart fences refuse cleanup. Pass an independent context,
// not a borrowed store lease. No Git command, queue mutation or acknowledgement
// occurs. Cancellation is checked between directories; a recursive removal may
// finish first. Power loss can resurrect deleted scratch; retry is safe.
func CleanupWorkspaces(ctx context.Context, req WorkspaceCleanup) (WorkspaceCleanupResult, error) {
	return cleanupWorkspaces(ctx, req, func(root *os.Root, name string) error { return root.RemoveAll(name) })
}

type workspaceRoot struct {
	path     string
	prefixes []string
	root     *os.Root
}
type workspaceCandidate struct {
	root *os.Root
	name string
	info os.FileInfo
}

func cleanupWorkspaces(ctx context.Context, req WorkspaceCleanup, remove func(*os.Root, string) error) (WorkspaceCleanupResult, error) {
	result := WorkspaceCleanupResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if storelock.SameActiveLease(ctx, ctx) {
		return result, fmt.Errorf("workspace cleanup requires an independent context")
	}
	paths := []string{req.StagingDir, req.Captures.Dir, req.Commits.Dir}
	for i, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) == filepath.Dir(filepath.Clean(path)) {
			return result, ErrInvalid
		}
		// Resolve existing ancestors as well as aliases of stores not yet created.
		canonical, err := cleanupPath(path)
		if err != nil {
			return result, err
		}
		paths[i] = canonical
	}
	for i := range paths {
		for j := 0; j < i; j++ {
			a, b := strings.ToLower(paths[i]), strings.ToLower(paths[j])
			rel, e := filepath.Rel(a, b)
			reverse, re := filepath.Rel(b, a)
			if (e == nil && filepath.IsLocal(rel)) || (re == nil && filepath.IsLocal(reverse)) {
				return result, ErrInvalid
			}
		}
	}
	st, err := os.Stat(paths[0])
	if err != nil {
		return result, err
	}
	if !st.IsDir() {
		return result, ErrInvalid
	}
	staging, release, err := storelock.Acquire(ctx, paths[0], 0)
	if err != nil {
		return result, err
	}
	defer release()
	roots := []workspaceRoot{
		{path: paths[1], prefixes: []string{".capture-work-"}},
		{path: filepath.Join(paths[1], "seeds"), prefixes: []string{".capture-work-"}},
		{path: paths[2], prefixes: []string{".capture-work-", ".publication-", ".startup-history-", ".confirmation-"}},
	}
	// Take all locks before inventory/deletion: a later busy/fenced store cannot
	// cause partial cleanup of an earlier store.
	for i := range roots {
		r := &roots[i]
		st, err := os.Lstat(r.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !st.IsDir() {
			return result, ErrInvalid
		} // includes linked seed substores
		st, err = directoryIdentity(os.Open(r.path))
		if err != nil {
			return result, err
		}
		_, release, err := storelock.Acquire(ctx, r.path, 0)
		if err != nil {
			return result, err
		}
		defer release()
		r.root, err = openWorkspaceRoot(r.path, st)
		if err != nil {
			return result, err
		}
		defer r.root.Close()
	}
	if req.Validate != nil {
		if err := req.Validate(staging); err != nil {
			return result, err
		}
	}
	var candidates []workspaceCandidate
	for _, r := range roots {
		if r.root == nil {
			continue
		}
		found, err := workspaceInventory(ctx, r)
		if err != nil {
			return result, err
		}
		candidates = append(candidates, found...)
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		st, err := c.root.Lstat(c.name)
		if err != nil {
			return result, err
		}
		if !st.IsDir() {
			return result, ErrInvalid
		}
		st, err = directoryIdentity(c.root.Open(c.name))
		if err != nil {
			return result, err
		}
		if !os.SameFile(st, c.info) {
			return result, ErrInvalid
		}
		if err := remove(c.root, c.name); err != nil {
			return result, err
		}
		result.RemovedWorkspaces++
	}
	return result, nil
}

func cleanupPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// A broken link is not an absent store.
	if st, e := os.Lstat(path); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = cleanupPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}

// Pin the directory observed before lock acquisition, not a replacement at the
// same path. This catches leaf/ancestor replacement across acquisition/opening;
// stable roots and ancestors remain a caller precondition throughout cleanup.
func openWorkspaceRoot(path string, expected os.FileInfo) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := directoryIdentity(root.Open("."))
	if err != nil || !os.SameFile(expected, actual) {
		root.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalid
	}
	return root, nil
}

// Stat through an open handle eagerly captures filesystem identity on Windows.
// Path-based Stat/Lstat and directory-entry information can defer identity lookup
// until SameFile, which would observe a later replacement at the original path.
// Close before returning so identity snapshots do not prevent Windows renames.
func directoryIdentity(directory *os.File, err error) (os.FileInfo, error) {
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrInvalid
	}
	return info, nil
}

func workspaceInventory(ctx context.Context, r workspaceRoot) ([]workspaceCandidate, error) {
	directory, err := r.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	var result []workspaceCandidate
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := directory.ReadDir(128)
		if err != nil && err != io.EOF {
			return nil, err
		}
		for _, entry := range entries {
			count++
			if count > 100000 {
				return nil, fmt.Errorf("workspace directory exceeds inventory entry limit")
			}
			for _, prefix := range r.prefixes {
				if !strings.HasPrefix(entry.Name(), prefix) || len(entry.Name()) == len(prefix) {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					return nil, err
				}
				if !info.IsDir() {
					return nil, ErrInvalid
				}
				info, err = directoryIdentity(r.root.Open(entry.Name()))
				if err != nil {
					return nil, err
				}
				result = append(result, workspaceCandidate{r.root, entry.Name(), info})
				break
			}
		}
		if err == io.EOF {
			return result, nil
		}
	}
}
