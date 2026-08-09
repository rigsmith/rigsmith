// Package gitrepo is rigsmith's git transport: a thin shell over the system git
// (matching core/gitutil's convention — no go-git), shared across the tools.
// rig's worktree/branch/prune commands drive it for worktree management, and
// clauderig drives it for the sync staging repo (a repo that's never ~/.claude
// itself, so redacted + slug-rewritten files are committed and pushed without
// secrets or transforms ever touching the live tree).
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return r.DirtyExcluding(ctx)
}

// DirtyExcluding is Dirty with the given paths ignored — for trees that carry
// bookkeeping the caller doesn't consider "changes", so an untracked line there
// doesn't read as unfinished work. Paths are relative to the repo root and
// exclude whole subtrees.
func (r *Repo) DirtyExcluding(ctx context.Context, exclude ...string) (bool, error) {
	args := []string{"status", "--porcelain"}
	if len(exclude) > 0 {
		args = append(args, "--", ".")
		for _, p := range exclude {
			args = append(args, ":(exclude)"+p)
		}
	}
	out, err := runGit(ctx, r.Dir, args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// AheadBehind counts the commits HEAD has that remote/branch does not, and vice
// versa. Local-only: it reads the remote-TRACKING ref, so it never touches the
// network and never blocks — which is what lets `status` and `doctor` call it.
//
// The consequence of that is worth stating: `behind` is only as fresh as the
// last fetch, so it is a lower bound. `ahead` does not have that problem, and
// ahead is the number that matters — commits that never left this machine.
//
// A missing remote-tracking ref (never fetched, or a remote configured but never
// pushed to) reports known=false. That third state matters in both directions:
// "cannot tell" must not render as lost work, and it must not render as "up to
// date with origin/main" either — a repo that has committed for a week against a
// remote it has never reached would otherwise look perfectly healthy.
func (r *Repo) AheadBehind(ctx context.Context, remote, branch string) (ahead, behind int, known bool, err error) {
	ref := remote + "/" + branch
	if _, verr := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", ref); verr != nil {
		return 0, 0, false, nil
	}
	out, err := runGit(ctx, r.Dir, "rev-list", "--left-right", "--count", ref+"...HEAD")
	if err != nil {
		return 0, 0, false, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false, fmt.Errorf("rev-list --count: unexpected output %q", strings.TrimSpace(out))
	}
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, false, err
	}
	if ahead, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, false, err
	}
	return ahead, behind, true, nil
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

// DirtyPaths lists the work-tree paths git reports as changed or untracked,
// for callers that need to know *what* is dirty rather than merely whether.
func (r *Repo) DirtyPaths(ctx context.Context) ([]string, error) {
	out, err := runGit(ctx, r.Dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	origin := false // the next record is a rename/copy source, not a status line
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		// Porcelain v1 -z reads "XY <path>", except that a rename or copy adds
		// the path it came from as its own status-less record: trimming three
		// bytes off that one would mangle the name rather than decode it.
		if origin {
			paths = append(paths, entry)
			origin = false
			continue
		}
		if len(entry) > 3 {
			paths = append(paths, entry[3:])
			origin = entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C'
			continue
		}
		paths = append(paths, entry)
	}
	return paths, nil
}

// Unborn reports whether HEAD points at a branch with no commits yet — a fresh
// `git init`. Merges behave differently there (git fast-forwards instead of
// creating a merge commit), so callers that need real merge topology check it.
func (r *Repo) Unborn(ctx context.Context) bool {
	_, err := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err != nil
}

// RevParse resolves a ref (branch, tag, HEAD, SHA) to its full commit hash.
func (r *Repo) RevParse(ctx context.Context, ref string) (string, error) {
	out, err := runGit(ctx, r.Dir, "rev-parse", ref)
	return strings.TrimSpace(out), err
}

// LastCommitMatching returns the newest commit whose message matches pattern as
// an extended regular expression, or "" when none does. Merges are included:
// callers looking for a commit a tool wrote itself need to find it wherever it
// sits in the history.
func (r *Repo) LastCommitMatching(ctx context.Context, pattern string) (string, error) {
	out, err := runGit(ctx, r.Dir, "log", "-1", "--format=%H", "-E", "--grep="+pattern)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
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
	return r.PushWithOptions(ctx, remote, branch)
}

// PushWithOptions is Push, sending each opt as a push option (`git push -o`).
// Servers that act on a push read them: josh, for one, refuses to create a ref
// it has never seen unless the push says which ref to base it on.
func (r *Repo) PushWithOptions(ctx context.Context, remote, branch string, opts ...string) error {
	args := []string{"push"}
	for _, o := range opts {
		args = append(args, "-o", o)
	}
	args = append(args, remote, "HEAD:"+branch)
	_, err := runGit(ctx, r.Dir, args...)
	return err
}

// FetchObjects fetches a single commit from a URL into the local object store,
// without touching any ref — enough to build on top of it.
func (r *Repo) FetchObjects(ctx context.Context, url, commit string) error {
	_, err := runGit(ctx, r.Dir, "fetch", "--no-tags", "--no-write-fetch-head", url, commit)
	return err
}

// CommitTree writes a commit with the given tree and single parent, touching no
// ref and no working tree. The caller decides where it goes.
func (r *Repo) CommitTree(ctx context.Context, tree, parent, message string) (string, error) {
	out, err := runGit(ctx, r.Dir, "commit-tree", tree, "-p", parent, "-m", message)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PushRef pushes an arbitrary commit to a remote ref.
func (r *Repo) PushRef(ctx context.Context, remote, commit, ref string) error {
	_, err := runGit(ctx, r.Dir, "push", remote, commit+":"+ref)
	return err
}

// PushRefForce replaces ref with commit under a lease: the push succeeds when
// the remote ref still holds what we last saw, and is refused when someone else
// has moved it. For callers that legitimately rewrite their own branch — a
// re-synthesized commit replacing the one they pushed before — where a plain
// push would be refused as non-fast-forward but a bare --force would be willing
// to discard another person's work.
func (r *Repo) PushRefForce(ctx context.Context, remote, commit, ref string) error {
	// The lease needs the ref's current value: pushing to a URL leaves no
	// remote-tracking ref for git to infer one from, so a bare
	// --force-with-lease=<ref> would have nothing to compare and refuse the
	// push. A ref that does not exist yet needs no lease at all — an ordinary
	// push already fails if someone creates it first.
	expected, err := r.lsRemoteOpt(ctx, remote, ref)
	if err != nil {
		return err
	}
	args := []string{"push"}
	if expected != "" {
		args = append(args, "--force-with-lease="+ref+":"+expected)
	}
	args = append(args, remote, commit+":"+ref)
	_, err = runGit(ctx, r.Dir, args...)
	return err
}

// CommitAmendNoEdit folds the currently staged changes into the last commit,
// keeping its message — for callers that must ship a metadata edit (e.g. a
// sync cursor) inside the commit that caused it, as one reviewable unit.
func (r *Repo) CommitAmendNoEdit(ctx context.Context) (string, error) {
	if _, err := runGit(ctx, r.Dir, "commit", "--amend", "--no-edit"); err != nil {
		return "", err
	}
	return r.Head(ctx)
}

// LsRemote resolves ref's SHA on a remote (a name or a URL) without fetching —
// the cheap "has upstream moved past our cursor" probe. ref not found on the
// remote is an error, not an empty result, so a typo'd branch is loud.
func (r *Repo) LsRemote(ctx context.Context, remote, ref string) (string, error) {
	sha, err := r.lsRemoteOpt(ctx, remote, ref)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", fmt.Errorf("ls-remote %s: ref %q not found", remote, ref)
	}
	return sha, nil
}

// ReplacePath makes dir's contents match what commit holds at that path,
// deleting anything present here and absent there. Both the index and the
// worktree are updated, so the result is ready to commit.
//
// Ordinary merging cannot do this: moving a directory *back* to an older
// revision is a merge with an ancestor, which is a no-op however much the trees
// differ.
func (r *Repo) ReplacePath(ctx context.Context, commit, dir string) error {
	// --ignore-unmatch: the path may not exist here at all, which is not an
	// error when the point is to make it match something else.
	if _, err := runGit(ctx, r.Dir, "rm", "-rq", "--ignore-unmatch", "--", dir); err != nil {
		return err
	}
	_, err := runGit(ctx, r.Dir, "checkout", commit, "--", dir)
	return err
}

// LsRemoteRefs resolves several refs on a remote in one round trip, returning
// ref name -> SHA for those that exist. A ref that is absent is simply missing
// from the map rather than an error: callers use this to ask which of a few
// candidate forms a name takes — a tag and its peeled commit, say — where "not
// this one" is the useful answer.
func (r *Repo) LsRemoteRefs(ctx context.Context, remote string, refs ...string) (map[string]string, error) {
	out, err := runGit(ctx, r.Dir, append([]string{"ls-remote", remote}, refs...)...)
	if err != nil {
		return nil, err
	}
	found := make(map[string]string, len(refs))
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && sha != "" {
			found[ref] = sha
		}
	}
	return found, nil
}

// lsRemoteOpt is LsRemote for callers that treat a missing ref as a fact rather
// than a failure — creating it, say — and still need a real error to surface
// when the remote itself is unreachable.
func (r *Repo) lsRemoteOpt(ctx context.Context, remote, ref string) (string, error) {
	out, err := runGit(ctx, r.Dir, "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
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

// gitExitCode runs git and returns its exit code (0 = success). It is for
// commands whose non-zero exit is a meaningful answer rather than a failure —
// e.g. `merge-base --is-ancestor` exits 1 for "no". A failure to even run git
// (binary missing, etc.) returns a negative code and the error.
func gitExitCode(ctx context.Context, dir string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	return runGitStdin(ctx, dir, "", nil, args...)
}

// runGitStdin is runGit with an optional stdin and extra environment. Secrets
// belong in env, never in args: the error below quotes every argument, and argv
// is readable by any process on the machine.
func runGitStdin(ctx context.Context, dir, stdin string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
