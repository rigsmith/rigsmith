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
	return r.fetchMerge(ctx, remote, branch, false, "", nil)
}

// FetchMergeUnrelated is FetchMerge for histories that share no common
// ancestor — the initial import of an external repo (e.g. through a josh
// filter), where git refuses a plain merge. msg, when non-empty, names the
// merge commit so imports read as imports in the log.
func (r *Repo) FetchMergeUnrelated(ctx context.Context, remote, branch, msg string, auth *HTTPAuth) (conflicted bool, err error) {
	return r.fetchMerge(ctx, remote, branch, true, msg, auth)
}

func (r *Repo) fetchMerge(ctx context.Context, remote, branch string, allowUnrelated bool, msg string, auth *HTTPAuth) (conflicted bool, err error) {
	if _, err := runGitStdin(ctx, r.Dir, "", auth.env(), "fetch", remote, branch); err != nil {
		return false, err
	}
	args := []string{"merge", "--no-edit"}
	if allowUnrelated {
		// --no-ff too: an import must land as a merge commit, and a repo
		// configured merge.ff=only would otherwise refuse the merge outright
		// (--allow-unrelated-histories does not override that policy).
		args = append(args, "--allow-unrelated-histories", "--no-ff")
	}
	if msg != "" {
		args = append(args, "-m", msg)
	}
	args = append(args, "FETCH_HEAD")
	if _, err := runGit(ctx, r.Dir, args...); err != nil {
		if unmerged, _ := runGit(ctx, r.Dir, "ls-files", "-u"); strings.TrimSpace(unmerged) != "" {
			return true, nil // genuine conflicts — repo is mid-merge
		}
		return false, err
	}
	return false, nil
}

// Fetch updates remote-tracking refs without touching the working tree, so a
// caller can measure divergence before deciding what to do about it.
func (r *Repo) Fetch(ctx context.Context, remote, branch string) error {
	_, err := runGit(ctx, r.Dir, "fetch", remote, branch)
	return err
}

// MergeRef merges ref into the current branch with an explicit merge commit.
// It returns conflicted=true (leaving the repo mid-merge for the caller to
// resolve) when the merge hit conflicts.
//
// Deliberately a merge and never a rebase: a rebase replays every sync commit
// and re-conflicts on the same files each time, so a 15-commit divergence means
// resolving the same MEMORY.md fifteen times. One merge resolves each file once.
func (r *Repo) MergeRef(ctx context.Context, ref string) (conflicted bool, err error) {
	if _, err := runGit(ctx, r.Dir, "merge", "--no-ff", "--no-edit", ref); err != nil {
		if unmerged, _ := r.UnmergedFiles(ctx); len(unmerged) > 0 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// MergeRefUncommitted leaves both clean and conflicted merges available for
// validation before CommitMerge. --no-ff also stops fast-forwards from moving
// HEAD before validation. An already integrated ref leaves no merge in progress.
func (r *Repo) MergeRefUncommitted(ctx context.Context, ref string) (conflicted bool, err error) {
	if _, err := runGit(ctx, r.Dir, "merge", "--no-ff", "--no-commit", "--no-edit", ref); err != nil {
		if unmerged, _ := r.UnmergedFiles(ctx); len(unmerged) > 0 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// FetchMergeUncommitted fetches before starting a merge that the caller must
// validate and commit. Existing auto-committing helpers retain their behavior.
func (r *Repo) FetchMergeUncommitted(ctx context.Context, remote, branch string) (bool, error) {
	if err := r.Fetch(ctx, remote, branch); err != nil {
		return false, err
	}
	return r.MergeRefUncommitted(ctx, "FETCH_HEAD")
}

// HasUnstagedChanges reports whether tracked working files differ from the
// index. A caller auditing working files must not commit different staged bytes.
func (r *Repo) HasUnstagedChanges(ctx context.Context) (bool, error) {
	out, err := runGit(ctx, r.Dir, "diff", "--name-only", "-z", "--")
	return out != "", err
}

// UnmergedFiles lists the paths left conflicted by an in-progress merge, as
// slash-relative repo paths.
func (r *Repo) UnmergedFiles(ctx context.Context) ([]string, error) {
	out, err := runGit(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// ConflictStages returns the three versions git recorded for a conflicted path:
// the common ancestor, ours, and theirs. A stage that doesn't exist (the file
// was added on one side, or deleted on the other) comes back nil rather than as
// an error — absence is a meaningful state to a resolver, not a failure.
func (r *Repo) ConflictStages(ctx context.Context, path string) (base, ours, theirs []byte, err error) {
	// ls-files -u lists one line per stage: "<mode> <sha> <stage>\t<path>".
	//
	// :(literal) because the argument after -- is still a pathspec, and a
	// conflicted filename containing *, ? or [ would otherwise match other
	// files — handing this resolver another file's stages under the name it
	// asked about, and writing that content into it.
	out, err := runGit(ctx, r.Dir, "ls-files", "-u", "-z", "--", ":(literal)"+path)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, rec := range strings.Split(out, "\x00") {
		meta, _, found := strings.Cut(rec, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		sha, stage := fields[1], fields[2]
		blob, err := runGit(ctx, r.Dir, "cat-file", "blob", sha)
		if err != nil {
			return nil, nil, nil, err
		}
		switch stage {
		case "1":
			base = []byte(blob)
		case "2":
			ours = []byte(blob)
		case "3":
			theirs = []byte(blob)
		}
	}
	return base, ours, theirs, nil
}

// AddPaths stages the given paths, marking a conflicted file resolved.
func (r *Repo) AddPaths(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := runGit(ctx, r.Dir, append([]string{"add", "--"}, paths...)...)
	return err
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
//
// Asked of git rather than by stat-ing .git/MERGE_HEAD: in a linked worktree
// .git is a FILE pointing at the real gitdir, so the stat silently answers "no
// merge" while git reports one. clauderig's staging repo is an ordinary clone, but
// this type is shared — rig's worktree verbs open it on worktree dirs — and a
// method that reads the layout instead of asking git is wrong there.
func (r *Repo) InMerge(ctx context.Context) bool {
	_, err := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD")
	return err == nil
}

// Conflicts lists the paths still unmerged in the index.
//
// NUL-delimited (-z) deliberately: without it git QUOTES any path with non-ASCII
// or special characters ("caf\303\251.md"), and that quoted display string
// would then be handed to ConflictStage/ResolveWith as if it were the real path
// — so exactly the conflicts hardest to resolve by hand would be the ones the
// policies silently could not resolve at all. Project slugs are derived from
// directory names, so non-ASCII is ordinary here, not exotic.
func (r *Repo) Conflicts(ctx context.Context) ([]string, error) {
	out, err := runGit(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
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
	// merge-file exits non-zero to COUNT remaining conflicts, and --union leaves
	// none — so a non-zero exit here is a real failure (binary input), not a
	// conflict count. It must be reported: merge-file writes its result over the
	// "ours" file IN PLACE, so on failure that file still holds the unmodified ours
	// side, and returning it would hand back one machine's copy under the label
	// "kept both machines' lines".
	if _, err := runGit(ctx, r.Dir, "merge-file", "--union", "-q", oursPath, basePath, theirsPath); err != nil {
		return nil, false
	}
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

// UnmergedPaths lists the paths still in conflict in a merge that stopped —
// what `git status` shows as U-something — slash-separated, sorted as git
// reports them. Empty when the index is clean.
func (r *Repo) UnmergedPaths(ctx context.Context) ([]string, error) { return r.Conflicts(ctx) }

// ResolveOurs settles each of the given conflicted paths in favour of this
// side: our version is restored and staged where we have one, and the path is
// removed where we do not (it was deleted by us, or exists only on theirs).
// The merge stays open for the caller to finish or abort.
func (r *Repo) ResolveOurs(ctx context.Context, paths []string) error {
	for _, p := range paths {
		// The paths came out of git verbatim, and go back in as the literal
		// names they are: `*` or `[` in a filename is not a pattern here.
		spec := ":(literal)" + p
		// Stage 2 is ours. Its absence means our side has no such file.
		stages, err := runGit(ctx, r.Dir, "ls-files", "-u", "-z", "--", spec)
		if err != nil {
			return err
		}
		if strings.Contains(stages, "\t") && hasStage(stages, 2) {
			if _, err := runGit(ctx, r.Dir, "checkout", "--ours", "--", spec); err != nil {
				return err
			}
			if _, err := runGit(ctx, r.Dir, "add", "--", spec); err != nil {
				return err
			}
			continue
		}
		if _, err := runGit(ctx, r.Dir, "rm", "-q", "-f", "--cached", "--", spec); err != nil {
			return err
		}
		// The index no longer knows the file; the working copy git left for
		// the conflict has to go too, or the next commit would stage it back.
		if err := os.Remove(filepath.Join(r.Dir, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// hasStage reports whether `ls-files -u -z` output carries an entry at the
// given stage. Each record reads "<mode> <sha> <stage>\t<path>"; only the
// part before the tab is looked at, since a path may itself contain " 2\t".
func hasStage(out string, stage int) bool {
	want := fmt.Sprintf(" %d", stage)
	for _, rec := range strings.Split(out, "\x00") {
		meta, _, ok := strings.Cut(rec, "\t")
		if ok && strings.HasSuffix(meta, want) {
			return true
		}
	}
	return false
}
