// Package gitrepo is clauderig's git transport: a thin shell over the system git
// (matching rigsmith's core/gitutil convention — no go-git) for the staging repo
// that sync pushes to and restore pulls from. Files are copied into the staging
// repo (already redacted + slug-rewritten), committed, and pushed; the repo is
// never ~/.claude itself, so secrets and transforms never touch the live tree.
package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git working tree at Dir.
type Repo struct {
	Dir string
}

// Open returns a Repo if dir is inside a git working tree.
func Open(ctx context.Context, dir string) (*Repo, error) {
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("gitrepo: %s is not a git repo: %w", dir, err)
	}
	return &Repo{Dir: dir}, nil
}

// Init ensures a git repo exists at dir (creating it on `main` with a clauderig
// identity and signing disabled — safe for non-interactive hook runs).
func Init(ctx context.Context, dir string) (*Repo, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err == nil {
		return &Repo{Dir: dir}, nil
	}
	if _, err := runGit(ctx, dir, "init", "-b", "main"); err != nil {
		return nil, err
	}
	_, _ = runGit(ctx, dir, "config", "commit.gpgsign", "false")
	// Set name and email independently so a partial global config (e.g. email set
	// but not name) can't cause "Please tell me who you are" on commit.
	if _, err := runGit(ctx, dir, "config", "user.email"); err != nil {
		_, _ = runGit(ctx, dir, "config", "user.email", "clauderig@localhost")
	}
	if _, err := runGit(ctx, dir, "config", "user.name"); err != nil {
		_, _ = runGit(ctx, dir, "config", "user.name", "clauderig")
	}
	return &Repo{Dir: dir}, nil
}

// Clone clones url into dir and returns the Repo.
func Clone(ctx context.Context, url, dir string) (*Repo, error) {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, parent, "clone", url, filepath.Base(dir)); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir}, nil
}

// SetRemote sets (or updates) a named remote URL.
func (r *Repo) SetRemote(ctx context.Context, name, url string) error {
	if r.HasRemote(ctx, name) {
		_, err := runGit(ctx, r.Dir, "remote", "set-url", name, url)
		return err
	}
	_, err := runGit(ctx, r.Dir, "remote", "add", name, url)
	return err
}

// HasRemote reports whether a named remote exists.
func (r *Repo) HasRemote(ctx context.Context, name string) bool {
	_, err := runGit(ctx, r.Dir, "remote", "get-url", name)
	return err == nil
}

// StageAll stages every change (additions, modifications, deletions).
func (r *Repo) StageAll(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "add", "-A")
	return err
}

// Dirty reports whether the working tree differs from HEAD (staged or not).
func (r *Repo) Dirty(ctx context.Context) (bool, error) {
	out, err := runGit(ctx, r.Dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Commit stages everything and commits with msg. It returns changed=false (and
// makes no commit) when the tree is clean — the empty-commit guard, so a no-op
// sync produces no noise. Signing is disabled for hook-safety.
func (r *Repo) Commit(ctx context.Context, msg string) (changed bool, err error) {
	if err := r.StageAll(ctx); err != nil {
		return false, err
	}
	dirty, err := r.Dirty(ctx)
	if err != nil || !dirty {
		return false, err
	}
	_, err = runGit(ctx, r.Dir, "-c", "commit.gpgsign=false", "commit", "-m", msg)
	return err == nil, err
}

// CurrentBranch returns the checked-out branch name.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// Head returns the HEAD commit hash.
func (r *Repo) Head(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.Dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Checkout switches to branch, creating/resetting it when create is set.
func (r *Repo) Checkout(ctx context.Context, branch string, create bool) error {
	args := []string{"checkout"}
	if create {
		args = append(args, "-B")
	}
	args = append(args, branch)
	_, err := runGit(ctx, r.Dir, args...)
	return err
}

// Push pushes the current HEAD to remote's branch.
func (r *Repo) Push(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "push", remote, "HEAD:"+branch)
	return err
}

// Pull fast-forwards the current branch from remote/branch. It is ff-only so a
// non-interactive (hook) pull never creates a merge commit or leaves conflicts;
// a non-ff divergence surfaces as an error for the caller to resolve.
func (r *Repo) Pull(ctx context.Context, remote, branch string) error {
	if _, err := runGit(ctx, r.Dir, "fetch", remote, branch); err != nil {
		return err
	}
	_, err := runGit(ctx, r.Dir, "merge", "--ff-only", "FETCH_HEAD")
	return err
}

// GitDirBytes is the on-disk size of the repo's .git directory — the input to the
// size-based history-squash decision.
func (r *Repo) GitDirBytes(ctx context.Context) (int64, error) {
	gd, err := runGit(ctx, r.Dir, "rev-parse", "--git-dir")
	if err != nil {
		return 0, err
	}
	path := strings.TrimSpace(gd)
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Dir, path)
	}
	return dirSize(path)
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
