package gitrepo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FetchMerge fetches remote/branch and merges it into the current branch (a real
// merge, not ff-only — used to reconcile when a push is rejected because the
// remote advanced). It returns conflicted=true (leaving the repo in the merge
// state for the caller to resolve) when the merge hit conflicts, or a non-nil
// error for any other failure.
func (r *Repo) FetchMerge(ctx context.Context, remote, branch string) (conflicted bool, err error) {
	if _, err := runGit(ctx, r.Dir, "fetch", remote, branch); err != nil {
		return false, err
	}
	if _, err := runGit(ctx, r.Dir, "merge", "--no-edit", "FETCH_HEAD"); err != nil {
		if unmerged, _ := runGit(ctx, r.Dir, "ls-files", "-u"); strings.TrimSpace(unmerged) != "" {
			return true, nil // genuine conflicts — repo is mid-merge
		}
		return false, err
	}
	return false, nil
}

// RunMergeTool launches the user's configured `git mergetool` interactively
// (inheriting the terminal). clauderig deliberately does not build a diff/merge
// UI — it hands off to whatever the user already uses.
func (r *Repo) RunMergeTool(ctx context.Context) error {
	return runGitInteractive(ctx, r.Dir, "mergetool")
}

// CommitMerge finishes an in-progress merge after conflicts are resolved.
func (r *Repo) CommitMerge(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "commit", "--no-edit")
	return err
}

// AbortMerge backs out an in-progress merge, restoring the pre-merge state.
func (r *Repo) AbortMerge(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "merge", "--abort")
	return err
}

// runGitInteractive runs git attached to the real terminal (for mergetool).
func runGitInteractive(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// InMerge reports whether a merge is in progress (MERGE_HEAD present). A staging
// repo left in this state wedges every later operation — an ff-only Pull fails
// with "unmerged files" and never recovers on its own — so callers check it on
// entry rather than only after a merge they started themselves.
func (r *Repo) InMerge(ctx context.Context) bool {
	_, err := os.Stat(filepath.Join(r.Dir, ".git", "MERGE_HEAD"))
	return err == nil
}

// Conflicts lists the paths still unmerged in the index.
func (r *Repo) Conflicts(ctx context.Context) ([]string, error) {
	out, err := runGit(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// ConflictStage returns one side of a conflicted path: stage 1 is the merge base,
// 2 "ours", 3 "theirs". ok is false when that stage is absent, which is normal and
// meaningful — an add/add conflict has no base, and a delete/modify conflict is
// missing whichever side deleted the file.
func (r *Repo) ConflictStage(ctx context.Context, path string, stage int) (content []byte, ok bool) {
	out, err := runGit(ctx, r.Dir, "show", fmt.Sprintf(":%d:%s", stage, path))
	if err != nil {
		return nil, false
	}
	return []byte(out), true
}

// ResolveWith writes content to a conflicted path and stages it, marking that
// path resolved.
func (r *Repo) ResolveWith(ctx context.Context, path string, content []byte) error {
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return err
	}
	_, err := runGit(ctx, r.Dir, "add", "--", path)
	return err
}

// UnionMerge resolves a conflicted TEXT path by keeping both sides of every
// conflicting hunk (git's own `merge-file --union`), which is the right answer for
// the append-shaped files clauderig syncs: transcripts and memory notes, where two
// machines each added their own lines and neither is a correction of the other.
// It returns ok=false for content git will not union (binary), leaving the path
// conflicted for the caller to handle.
func (r *Repo) UnionMerge(ctx context.Context, path string) (content []byte, ok bool) {
	base, hasBase := r.ConflictStage(ctx, path, 1)
	ours, hasOurs := r.ConflictStage(ctx, path, 2)
	theirs, hasTheirs := r.ConflictStage(ctx, path, 3)
	if !hasOurs || !hasTheirs {
		return nil, false // delete/modify — not a text union
	}
	if !hasBase {
		base = nil
	}
	dir, err := os.MkdirTemp("", "clauderig-union")
	if err != nil {
		return nil, false
	}
	defer os.RemoveAll(dir)
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return ""
		}
		return p
	}
	oursPath, basePath, theirsPath := write("ours", ours), write("base", base), write("theirs", theirs)
	if oursPath == "" || basePath == "" || theirsPath == "" {
		return nil, false
	}
	// merge-file exits non-zero to COUNT remaining conflicts; --union leaves none,
	// so only a real failure (binary input) matters — detected by an empty result.
	_, _ = runGit(ctx, r.Dir, "merge-file", "--union", "-q", oursPath, basePath, theirsPath)
	merged, err := os.ReadFile(oursPath)
	if err != nil {
		return nil, false
	}
	return merged, true
}

// SideCommitTime returns when the given ref last touched path. It is how a
// snapshot repo decides a whole-file conflict without knowing the file's format:
// both sides are machine snapshots of the same thing, so the one committed later
// is the later truth. A path the ref never touched reports ok=false.
func (r *Repo) SideCommitTime(ctx context.Context, ref, path string) (t time.Time, ok bool) {
	out, err := runGit(ctx, r.Dir, "log", "-1", "--format=%cI", ref, "--", path)
	if err != nil {
		return time.Time{}, false
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return time.Time{}, false
	}
	t, err = time.Parse(time.RFC3339, s)
	return t, err == nil
}
