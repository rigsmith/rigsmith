package commitartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

// MergePlanPolicy supplies the existing bounded content resolver and raw-tree
// checks. MaxBundleBytes bounds the returned bundle; zero uses the archive limit.
// Parent-tip and candidate audits do not certify every historical ancestor.
type MergePlanPolicy struct {
	MergeFinishPolicy
	Resolve        ResolveConflict
	MaxBundleBytes int64
}

// MergePlan describes a private, self-contained candidate, not an applied repair
// or a durable queue receipt. The caller owns the new output directory. Applying
// a plan still requires fresh state checks and safe index/worktree installation;
// IndexDigest alone does not establish that live worktree files are unchanged.
type MergePlan struct {
	Original, Incoming, Commit, Tree string
	IndexDigest, BundlePath          string
}

// PlanUnresolvedMerge recreates an unresolved two-parent merge in a private
// repository, resolves supported content conflicts, audits both tips and the
// candidate, and returns a verified bundle in a new destination directory.
// The canonical index must match the recreated merge's entries exactly: partial
// resolutions and additional staged edits are declined. Live worktree files are
// never read or changed. Callers hold the cooperative staging lease throughout.
// No canonical refs, index, operation metadata or objects are written. Failed
// calls remove only their own output directory; existing destinations are refused.
func PlanUnresolvedMerge(ctx context.Context, dir, dest string, p MergePlanPolicy) (result MergePlan, err error) {
	if !filepath.IsAbs(dir) || !filepath.IsAbs(dest) || p.Resolve == nil || p.MaxTreeBytes < 0 || p.MaxBundleBytes < 0 {
		return result, ErrInvalid
	}
	if _, err := requestKey(Request{PolicyID: "unresolved-merge-plan-v1", Message: p.Message,
		AuthorName: p.AuthorName, AuthorEmail: p.AuthorEmail, Time: p.Time,
		Prepare: p.Validate, Audit: p.Audit}); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	source := gitRepo{dir: dir}
	state, err := source.unresolvedPlanState(ctx)
	if err != nil {
		return result, err
	}
	dest, err = source.planDestination(ctx, dest)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
			result = MergePlan{}
		}
	}()
	work, err := source.mergeWorkDir(ctx, dest, ".work-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	indexCopy := filepath.Join(work, "index")
	if err := os.WriteFile(indexCopy, state.index, 0600); err != nil {
		return result, err
	}
	indexed := source
	indexed.identity = []string{"GIT_INDEX_FILE=" + indexCopy}
	actual, err := indexed.run(ctx, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return result, err
	}
	repo, err := initRepo(ctx, filepath.Join(work, "git"), state.original)
	if err != nil {
		return result, err
	}
	for i, head := range []string{state.original, state.incoming} {
		if err := repo.importRef(ctx, dir, head, fmt.Sprintf("refs/rig/plan-parent-%d", i), head); err != nil {
			return result, err
		}
	}
	if err := repo.completeHistory(ctx); err != nil {
		return result, err
	}
	if err := repo.prepareTextMerge(ctx); err != nil {
		return result, err
	}
	var raw bytes.Buffer
	mergeErr := repo.runTo(ctx, nil, &boundedOutput{w: &raw, left: gitOutputLimit},
		"-c", "merge.renames=false", "merge-tree", "--write-tree", "-z", "--no-messages", state.original, state.incoming)
	if mergeErr == nil {
		return result, ErrConflict // No unresolved content merge to plan.
	}
	if !gitExited(mergeErr, 1) {
		return result, mergeErr
	}
	tree, conflicts, err := parseConflicts(raw.String())
	if err != nil {
		return result, err
	}
	// Recreate stage 0 and conflict stages in the private index before trusting
	// the live index's provenance. Never overwrite a staged manual resolution.
	if _, err := repo.run(ctx, nil, "read-tree", tree); err != nil {
		return result, err
	}
	var stages strings.Builder
	for _, conflict := range conflicts {
		fmt.Fprintf(&stages, "0 %s\t%s\x00", strings.Repeat("0", len(tree)), conflict.path)
		for i, oid := range conflict.oids {
			if oid != "" {
				fmt.Fprintf(&stages, "%s %s %d\t%s\x00", conflict.mode, oid, i+1, conflict.path)
			}
		}
	}
	if _, err := repo.run(ctx, strings.NewReader(stages.String()), "update-index", "-z", "--index-info"); err != nil {
		return result, err
	}
	expected, err := repo.run(ctx, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return result, err
	}
	if actual != expected {
		return result, ErrConflict
	}
	tree, err = repo.resolveConflicts(ctx, raw.String(), state.original, state.incoming, p.Resolve)
	if err != nil {
		return result, err
	}
	tree = strings.TrimSpace(tree)
	if !objectID(tree) {
		return result, ErrInvalid
	}
	for _, candidate := range []string{state.original, state.incoming, tree} {
		if err := repo.checkTree(ctx, candidate, work, p.MaxTreeBytes, p.Validate, p.Audit); err != nil {
			return result, err
		}
	}
	repo.identity = []string{"GIT_AUTHOR_NAME=" + p.AuthorName, "GIT_AUTHOR_EMAIL=" + p.AuthorEmail,
		"GIT_COMMITTER_NAME=" + p.AuthorName, "GIT_COMMITTER_EMAIL=" + p.AuthorEmail,
		fmt.Sprintf("GIT_AUTHOR_DATE=@%d +0000", p.Time.Unix()), fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", p.Time.Unix())}
	commit, err := repo.run(ctx, strings.NewReader(p.Message+"\n"), "commit-tree", tree, "-p", state.original, "-p", state.incoming)
	if err != nil {
		return result, err
	}
	commit = strings.TrimSpace(commit)
	if !objectID(commit) {
		return result, ErrInvalid
	}
	const planRef = "refs/rig/merge-plan"
	if _, err := repo.run(ctx, nil, "update-ref", planRef, commit); err != nil {
		return result, err
	}
	limit := p.MaxBundleBytes
	if limit == 0 {
		limit = artifact.DefaultMaxBytes
	}
	bundle := filepath.Join(dest, "merge.bundle")
	f, err := os.OpenFile(bundle, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	err = repo.runTo(ctx, nil, &boundedOutput{w: f, left: limit}, "bundle", "create", "-", planRef)
	if err = errors.Join(err, f.Close()); err != nil {
		return result, err
	}
	verify, err := initRepo(ctx, filepath.Join(work, "verify"), commit)
	if err != nil {
		return result, err
	}
	if err := verify.importRef(ctx, bundle, planRef, planRef, commit); err != nil {
		return result, err
	}
	if err := verify.completeHistory(ctx); err != nil {
		return result, err
	}
	current, err := source.unresolvedPlanState(ctx)
	if err != nil {
		return result, err
	}
	if !state.equal(current) {
		return result, ErrConflict
	}
	digest := sha256.Sum256(state.index)
	return MergePlan{Original: state.original, Incoming: state.incoming, Commit: commit, Tree: tree,
		IndexDigest: hex.EncodeToString(digest[:]), BundlePath: bundle}, nil
}

type mergePlanState struct {
	original, incoming       string
	index, head, merge, orig []byte
}

func (s mergePlanState) equal(other mergePlanState) bool {
	return s.original == other.original && s.incoming == other.incoming && bytes.Equal(s.index, other.index) &&
		bytes.Equal(s.head, other.head) && bytes.Equal(s.merge, other.merge) && bytes.Equal(s.orig, other.orig)
}

func (r gitRepo) unresolvedPlanState(ctx context.Context) (s mergePlanState, err error) {
	for _, name := range []string{"CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "MERGE_AUTOSTASH", "BISECT_START"} {
		path, err := r.operationPath(ctx, name)
		if err != nil {
			return s, err
		}
		if _, err := os.Lstat(path); err == nil {
			return s, ErrConflict
		} else if !os.IsNotExist(err) {
			return s, err
		}
	}
	for _, file := range []struct {
		name  string
		limit int64
		data  *[]byte
	}{
		{"MERGE_HEAD", 256, &s.merge}, {"ORIG_HEAD", 256, &s.orig}, {"HEAD", 4096, &s.head}, {"index", 64 << 20, &s.index},
	} {
		path, err := r.operationPath(ctx, file.name)
		if err != nil {
			return s, err
		}
		*file.data, err = readMergeFile(path, file.limit)
		if err != nil {
			return s, fmt.Errorf("%w: unavailable merge state: %w", ErrConflict, err)
		}
	}
	s.original, s.incoming = strings.TrimSuffix(string(s.orig), "\n"), strings.TrimSuffix(string(s.merge), "\n")
	if !objectID(s.original) || !objectID(s.incoming) || s.original == s.incoming {
		return s, ErrConflict
	}
	head, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return s, err
	}
	if strings.TrimSpace(head) != s.original {
		return s, ErrConflict
	}
	return s, nil
}

// Resolve destination parents before writing, including linked-worktree Git
// metadata and all registered checkouts. Cooperative ownership excludes parent swaps.
func (r gitRepo) planDestination(ctx context.Context, dest string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return "", err
	}
	dest = filepath.Join(parent, filepath.Base(dest))
	roots := []string{r.dir}
	// Use the isolated, bounded Git runner and NUL records so unusual checkout
	// names cannot escape the guard through porcelain quoting or line breaks.
	worktrees, err := r.run(ctx, nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Split(worktrees, "\x00") {
		if root, ok := strings.CutPrefix(field, "worktree "); ok {
			if !filepath.IsAbs(root) {
				return "", ErrInvalid
			}
			roots = append(roots, root)
		}
	}
	for _, args := range [][]string{{"rev-parse", "--absolute-git-dir"}, {"rev-parse", "--git-common-dir"}} {
		root, err := r.run(ctx, nil, args...)
		if err != nil {
			return "", err
		}
		root = strings.TrimSpace(root)
		if !filepath.IsAbs(root) {
			root = filepath.Join(r.dir, root)
		}
		roots = append(roots, root)
	}
	// String containment alone misses filesystem aliases (for example Unicode
	// normalization on macOS). The destination is new, so inspect its existing
	// ancestors and compare their identities with every protected root.
	var ancestors []os.FileInfo
	for path := parent; ; path = filepath.Dir(path) {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		ancestors = append(ancestors, info)
		if filepath.Dir(path) == path {
			break
		}
	}
	for _, root := range roots {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		if mergePlanPathsOverlap(root, dest) {
			return "", ErrInvalid
		}
		info, err := os.Stat(root)
		if err != nil {
			return "", err
		}
		for _, ancestor := range ancestors {
			if os.SameFile(info, ancestor) {
				return "", ErrInvalid
			}
		}
	}
	return dest, nil
}

// Compare path components so volume roots and sibling prefixes remain distinct.
func mergePlanPathsOverlap(a, b string) bool {
	// Fold on every host to reject ambiguous case aliases conservatively.
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	within := func(root, path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}
