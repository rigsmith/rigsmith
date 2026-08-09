package gitrepo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// LastCommit returns the short hash, subject, and relative time of HEAD.
func (r *Repo) LastCommit(ctx context.Context) (hash, subject, when string, err error) {
	out, err := runGit(ctx, r.Dir, "log", "-1", "--format=%h%x1f%s%x1f%cr")
	if err != nil {
		return "", "", "", err
	}
	parts := strings.SplitN(strings.TrimRight(out, "\n"), "\x1f", 3)
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("gitrepo: unexpected log output %q", out)
	}
	return parts[0], parts[1], parts[2], nil
}

// Reachable reports whether url responds to ls-remote (network check). An empty
// but reachable remote still counts as reachable.
func Reachable(ctx context.Context, url string) bool {
	_, err := runGit(ctx, "", "ls-remote", url)
	return err == nil
}

// ShowFile returns a file's contents at ref, without touching the working tree
// — the object store already holds everything a fetch brought down, so a peer's
// files are readable long before (or without) any merge.
func (r *Repo) ShowFile(ctx context.Context, ref, path string) ([]byte, error) {
	out, err := runGit(ctx, r.Dir, "show", ref+":"+path)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// LogNameOnly returns `git log` over ref limited to pathspec, with the given
// format followed by each commit's changed files. One walk answers both "when
// did each path last change" and "which commit did it", which per-file queries
// would otherwise cost one process each.
func (r *Repo) LogNameOnly(ctx context.Context, ref, format, pathspec string) (string, error) {
	args := []string{"log", "--format=" + format, "--name-only", ref}
	if pathspec != "" {
		args = append(args, "--", pathspec)
	}
	return runGit(ctx, r.Dir, args...)
}

// Divergence is HEAD's position relative to a tracking ref, as of the last
// fetch. Ahead/Behind are commit counts; Conflict answers "would merging that
// ref leave conflicts" without touching the worktree.
type Divergence struct {
	Ref      string `json:"ref"`      // the ref compared against, e.g. "origin/main"
	Tracked  bool   `json:"tracked"`  // Ref resolves locally — false means never fetched
	Ahead    int    `json:"ahead"`    // commits on HEAD that Ref lacks
	Behind   int    `json:"behind"`   // commits on Ref that HEAD lacks
	Conflict bool   `json:"conflict"` // merging Ref would leave conflicts
	Merging  bool   `json:"merging"`  // an unresolved merge is sitting in the repo
}

// Diverged reports whether both sides moved — the state a ff-only pull cannot
// resolve, and the one the 2026-08-07 incident left invisible for a day.
func (d Divergence) Diverged() bool { return d.Ahead > 0 && d.Behind > 0 }

// DivergenceFrom compares HEAD against ref (e.g. "origin/main") using only what
// is already in the object store — it never touches the network, so callers can
// poll it. An unresolvable ref yields Tracked=false rather than an error, since
// "never fetched" is a state to render, not a failure.
func (r *Repo) DivergenceFrom(ctx context.Context, ref string) (Divergence, error) {
	d := Divergence{Ref: ref}
	d.Merging = r.mergeInProgress(ctx)

	if _, err := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return d, nil // not fetched yet — nothing to compare against
	}
	d.Tracked = true

	out, err := runGit(ctx, r.Dir, "rev-list", "--left-right", "--count", "HEAD..."+ref)
	if err != nil {
		return d, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return d, fmt.Errorf("gitrepo: unexpected rev-list output %q", out)
	}
	if d.Ahead, err = strconv.Atoi(fields[0]); err != nil {
		return d, fmt.Errorf("gitrepo: rev-list ahead %q: %w", fields[0], err)
	}
	if d.Behind, err = strconv.Atoi(fields[1]); err != nil {
		return d, fmt.Errorf("gitrepo: rev-list behind %q: %w", fields[1], err)
	}

	// Only a two-sided divergence can conflict: behind-only fast-forwards and
	// ahead-only has nothing to merge. Skipping the probe keeps the common
	// (healthy) poll to a single rev-list.
	if d.Diverged() {
		d.Conflict = r.wouldConflict(ctx, ref)
	}
	return d, nil
}

// wouldConflict probes the merge in-memory via `merge-tree --write-tree`, which
// writes objects but never the index or worktree. Exit 1 means conflicts; any
// other failure (ancient git, unrelated histories) reports no conflict and lets
// the real merge speak for itself.
func (r *Repo) wouldConflict(ctx context.Context, ref string) bool {
	code, err := gitExitCode(ctx, r.Dir, "merge-tree", "--write-tree", "HEAD", ref)
	return err == nil && code == 1
}

// mergeInProgress reports whether the repo is sitting mid-merge — the residue a
// conflicted pull leaves behind.
func (r *Repo) mergeInProgress(ctx context.Context) bool {
	_, err := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD")
	return err == nil
}
