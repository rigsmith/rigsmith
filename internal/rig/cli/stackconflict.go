package cli

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// stackSettleConflicts handles a pull whose merge stopped on conflicts.
//
// A history fetched through `:prefix=<name>` has no say about anything outside
// <name>/: every path it carries is under the prefix, so a conflict reported
// elsewhere can only mean the merge base already held that path — the fetched
// history shares an ancestor with this stackspace that was a full stackspace
// commit, not a filtered one. Whatever put it there, the answer for those
// paths is always this stackspace's own version, so they are settled that way
// here rather than handed to the user as eighty conflicts in the wrong
// directory. Conflicts inside the prefix are real, and stay.
//
// It reports what it settled and what remains, by prefix, and commits the
// merge when nothing remains.
func stackSettleConflicts(ctx context.Context, out io.Writer, repo *gitrepo.Repo, name string) error {
	paths, err := repo.UnmergedPaths(ctx)
	if err != nil {
		return err
	}
	var inside, outside []string
	for _, p := range paths {
		if p == name || strings.HasPrefix(p, name+"/") {
			inside = append(inside, p)
		} else {
			outside = append(outside, p)
		}
	}
	if len(outside) > 0 {
		if err := repo.ResolveOurs(ctx, outside); err != nil {
			return fmt.Errorf("settling conflicts outside %s/: %w", name, err)
		}
		fmt.Fprintf(out, "%s: %d conflict(s) outside %s/ settled as this stackspace's version — %s\n",
			name, len(outside), name, stackConflictDirs(outside))
		fmt.Fprintf(out, "  a history filtered to %s/ cannot change anything outside it, so those were never upstream's to decide;\n"+
			"  their appearing at all means the fetched history shares a stackspace commit as an ancestor — `git merge-base HEAD FETCH_HEAD` names it\n", name)
	}
	if len(inside) > 0 {
		shown := inside
		more := ""
		if len(shown) > 8 {
			shown, more = shown[:8], fmt.Sprintf("\n  … and %d more", len(inside)-8)
		}
		return fmt.Errorf("merge conflicts under %s/ (%d file(s)):\n  %s%s\nresolve them, commit, then re-run to move the cursor — or `git merge --abort` to step back",
			name, len(inside), strings.Join(shown, "\n  "), more)
	}
	if err := repo.CommitMerge(ctx); err != nil {
		return fmt.Errorf("committing the settled merge: %w", err)
	}
	return nil
}

// stackConflictDirs summarises paths by their top-level directory, so eighty
// files read as "mermaider/ (80)" rather than as eighty lines.
func stackConflictDirs(paths []string) string {
	counts := map[string]int{}
	for _, p := range paths {
		top := p
		if i := strings.Index(p, "/"); i >= 0 {
			top = p[:i] + "/"
		} else {
			top = path.Base(p)
		}
		counts[top]++
	}
	dirs := make([]string, 0, len(counts))
	for d := range counts {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	parts := make([]string, 0, len(dirs))
	for _, d := range dirs {
		parts = append(parts, fmt.Sprintf("%s (%d)", d, counts[d]))
	}
	return strings.Join(parts, ", ")
}
