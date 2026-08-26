package cli

// Nested git worktrees — a second checkout of the repo living *inside* the repo
// tree (.claude/worktrees/<branch>, wt/<branch>, a hand-made `git worktree add
// ./tmp`) — hold a complete copy of every project. Left in discovery they
// duplicate every name in the workspace, which turns `rig run <name>` ambiguous
// and, worse, lets a bare `rig run` launch a stale copy that looks exactly like
// the real app. rig knows where they are, so it skips them by default;
// `--include-worktrees` opts back in for the rare cross-worktree sweep.
//
// Detection is filesystem-level rather than a `git worktree list` call: a linked
// worktree's root holds a `.git` FILE (not a directory) pointing at the admin
// dir the parent repo keeps under `.git/worktrees/<name>`. That marker is what
// git itself keys off, it costs a stat, and it recognises worktrees of *other*
// repos too. A submodule also has a `.git` file, but it points into
// `.git/modules/…` — so submodules stay discoverable.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// includeWorktrees is the `--include-worktrees` persistent flag: keep projects
// that live inside nested git worktrees in discovery.
var includeWorktrees bool

// nestedWorktree is one linked git worktree checked out inside the repo.
type nestedWorktree struct {
	Dir    string // absolute
	Rel    string // repo-relative, slash-separated
	Branch string // short branch name, "" when detached or unknown
	Merged bool   // branch already contained in the repo's mainline
	// State is the human clause describing what `rig prune` would do with it
	// ("already merged — `rig prune` removes it", the reason prune would keep
	// it anyway, or "" when it isn't merged and there is nothing to claim).
	State string
}

// dropNestedWorktrees removes the targets that live inside a nested git
// worktree — the default for every discovery path. It is a no-op under
// `--include-worktrees`, and never drops anything at the repo root itself.
func dropNestedWorktrees(root string, ts []target) []target {
	if includeWorktrees {
		return ts
	}
	out := ts[:0:0]
	for _, t := range ts {
		if _, nested := nestedWorktreeFor(root, t.Dir); nested {
			continue
		}
		out = append(out, t)
	}
	return out
}

// inNestedWorktree reports whether a repo-relative slash path lives inside a
// nested git worktree (false under `--include-worktrees`). Used where discovery
// works in relative paths — the per-binary Go main expansion.
func inNestedWorktree(root, rel string) bool {
	if includeWorktrees || rel == "" || rel == "." {
		return false
	}
	_, nested := nestedWorktreeFor(root, filepath.Join(root, filepath.FromSlash(rel)))
	return nested
}

// nestedWorktreeFor returns the root of the nested worktree containing dir, if
// any: it walks up from dir to (but not including) root, looking for the linked
// worktree marker. The repo root is never itself "nested" — the primary
// checkout is where we are working from.
func nestedWorktreeFor(root, dir string) (string, bool) {
	root = filepath.Clean(root)
	for d := filepath.Clean(dir); d != root && d != "" && d != string(filepath.Separator); d = filepath.Dir(d) {
		if walkutil.LinkedWorktreeRoot(d) {
			return d, true
		}
		if parent := filepath.Dir(d); parent == d {
			break // reached the volume root without meeting the repo root
		}
	}
	return "", false
}

// nestedWorktrees lists the repo's linked worktrees that sit inside root, in
// `git worktree list` order, each with the prune verdict for its branch (see
// pruneState). Best-effort: any git hiccup yields no worktrees rather than an
// error.
func nestedWorktrees(ctx context.Context, root string) []nestedWorktree {
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return nil
	}
	wts, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil
	}
	var base, baseSHA string
	var out []nestedWorktree
	// git reports symlink-free paths, so resolve the root the same way before
	// asking whether a worktree is inside it — on macOS /var and temp dirs live
	// behind /private symlinks, and a raw compare would call every worktree
	// external.
	realRoot := resolveDir(root)
	for _, w := range wts {
		rel, err := filepath.Rel(realRoot, resolveDir(w.Path))
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue // the primary checkout, or a sibling worktree — not nested
		}
		n := nestedWorktree{Dir: w.Path, Rel: filepath.ToSlash(rel), Branch: w.Branch}
		if n.Branch != "" {
			if base == "" {
				base = repo.DefaultBranch(ctx)
				baseSHA, _ = repo.RevParse(ctx, base)
			}
			n.Merged, n.State = pruneState(ctx, repo, w, base, baseSHA)
		}
		out = append(out, n)
	}
	return out
}

// pruneState answers what `rig prune` would actually do with a worktree, in the
// clause the hints print. "Merged" alone is not the same as "prune removes it":
// prune also keeps a worktree with uncommitted changes, and keeps one whose
// branch sits at base having never advanced (brand-new, nothing to prune). It
// mirrors pruneSweep's decision so the two never disagree — promising a removal
// that prune then declines is worse than saying nothing.
func pruneState(ctx context.Context, repo *gitrepo.Repo, w gitrepo.Worktree, base, baseSHA string) (merged bool, state string) {
	merged, err := repo.IsMerged(ctx, w.Branch, base)
	if err != nil || !merged {
		return false, ""
	}
	if clean, err := repo.WorktreeClean(ctx, w.Path); err != nil || !clean {
		return true, "is already merged but has uncommitted changes — `rig prune` keeps it"
	}
	if baseSHA != "" && w.Head == baseSHA {
		if advanced, err := repo.BranchAdvanced(ctx, w.Branch); err != nil || !advanced {
			return true, "is even with " + base + " — `rig prune` keeps it"
		}
	}
	return true, "is already merged — `rig prune` removes it"
}

// nestedWorktreeNote is the one-line "this copy is a nested worktree" hint for
// a path that shadows a project, or "" when the path isn't in one. A merged
// worktree names the command that removes it — the wt → prune loop that
// otherwise never closes.
func nestedWorktreeNote(ctx context.Context, root, dir string) string {
	// nestedWorktreeFor deliberately ignores `--include-worktrees` here: the hint
	// describes what a path *is*, whether or not discovery is filtering it out.
	wtDir, ok := nestedWorktreeFor(root, dir)
	if !ok {
		return ""
	}
	for _, w := range nestedWorktrees(ctx, root) {
		if !sameDir(w.Dir, wtDir) {
			continue
		}
		what := "nested worktree " + w.Rel
		if w.Branch != "" {
			what += " (" + w.Branch + ")"
		}
		if w.State != "" {
			return what + " " + w.State
		}
		return what
	}
	return "nested worktree " + relSlash(root, wtDir)
}
