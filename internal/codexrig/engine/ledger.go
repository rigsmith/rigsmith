package engine

import (
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/ledger"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// recordLedger walks the STAGED tree — not the live one — and remembers every
// session in it.
//
// The staged tree is the right source for two reasons. It holds every machine's
// rollouts, not only this one's, so a session that reached the repo from
// somewhere else is remembered here too. And it is what retention is about to
// prune, so recording from it is recording exactly what is about to become
// unfindable.
func recordLedger(staging, device string) (added, total int, err error) {
	l, err := ledger.Open(staging, device)
	if err != nil {
		return 0, 0, err
	}
	roots, err := os.ReadDir(staging)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	for _, rootDir := range roots {
		if !rootDir.IsDir() || rootDir.Name() == ".git" || rootDir.Name() == ledger.DirName {
			continue
		}
		base := filepath.Join(staging, rootDir.Name())
		werr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(base, p)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if !rollout.IsRolloutRel(rel) {
				return nil
			}
			id := rollout.IDFromRolloutRel(rel)
			if id == "" {
				return nil
			}
			// rolloutstore.Stat, not d.Info(): past the chunking threshold the
			// file on disk is a small index, and the ledger would record — and
			// fingerprint freshness by — the index's size, not the conversation's.
			info, ierr := rolloutstore.Stat(p)
			if ierr != nil {
				return nil
			}

			// The cheap test first: a rollout whose size and end time are
			// already recorded cannot have a different row, and re-reading
			// every staged session on every sync would put a tail read per
			// conversation into the hot path.
			act, haveAct := rollout.LastActivity(p)
			if haveAct && l.Fresh(id, act.At, info.Size()) {
				return nil
			}

			e := ledger.Entry{ID: id, Bytes: info.Size(), Shard: shardOf(rel)}
			if haveAct {
				e.End = act.At
			}
			if meta, ok, _ := rollout.ReadMeta(p); ok {
				e.Started, e.Cwd, e.Branch, e.CLIVersion = meta.At, meta.Cwd, meta.Branch, meta.CLIVersion
			}
			e.Title = rollout.FirstPrompt(p)
			if l.Note(e) {
				added++
			}
			return nil
		})
		if werr != nil {
			return added, l.Count(), werr
		}
	}
	if err := l.Save(); err != nil {
		return added, l.Count(), err
	}
	return added, l.Count(), nil
}

// shardOf is the rollout's directory, which is where to look in git history.
// Not named `path`: a package-level identifier with an imported package's name
// blocks that import for every file in the package.
func shardOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i > 0 {
		return rel[:i]
	}
	return ""
}
