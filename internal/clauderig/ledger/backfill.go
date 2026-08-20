package ledger

import (
	"context"
	"strings"
	"time"
)

// headBytes is how much of a deleted transcript the backfill reads. The cwd and
// the first human prompt sit in the first few records; the rest can be tens of
// megabytes and holds nothing a ledger row wants.
const headBytes = 1 << 20

// Deletion is a transcript git no longer tracks, and the commit that removed it.
type Deletion struct {
	Path   string
	Commit string
}

// History is the slice of git the backfill needs. An interface, so the recovery
// logic is testable without a repo — and so this package keeps knowing nothing
// about how git is driven.
type History interface {
	Deletions(ctx context.Context, pathspec string) ([]Deletion, error)
	LastCommitTime(ctx context.Context, rev, path string) (time.Time, error)
	ShowPrefix(ctx context.Context, rev, path string, max int) ([]byte, error)
}

// Parse extracts a title and cwd from a transcript's leading bytes. Injected so
// this package doesn't need the transcript-format readers.
type Parse func(head []byte) (title, cwd string)

// BackfillResult reports what a backfill did. Skipped and Unreadable are counted
// rather than folded into a total: "550 already known" and "3 unreadable" mean
// very different things about a repo, and a single number hides both.
type BackfillResult struct {
	Deleted    int // deleted transcripts found in history
	Recovered  int // rows written
	Skipped    int // already in the ledger
	Unreadable int // blob or metadata could not be read
}

// Backfill recovers ledger rows for transcripts that have already been pruned
// out of the synced tree, reading them from the sync repo's git history.
//
// It exists because the ledger only starts remembering when it is installed: the
// sessions that aged out before it existed are exactly the ones with no row, and
// their bodies are still in history. Rows already present are never overwritten —
// a live transcript is a better source than a deleted blob.
//
// What it can see is bounded by what history still holds: `sync` squashes the
// staging repo once it grows past the size floor, and that prunes unreachable
// blobs, so anything dropped before the last squash is gone for good. Backfill
// reports what it found rather than promising a complete recovery.
func Backfill(ctx context.Context, l *Ledger, h History, parse Parse) (BackfillResult, error) {
	var res BackfillResult
	dels, err := h.Deletions(ctx, "cli/projects")
	if err != nil {
		return res, err
	}
	// Known ids come from EVERY device's shard, not just this one's. Rows are
	// unioned on read, so a session another machine already recovered is not new
	// here — treating it as new would re-read every blob and write a duplicate row
	// that then wins on recency while saying nothing new.
	known := LoadAll(l.dir)
	for _, d := range dels {
		rel, ok := cliRel(d.Path)
		if !ok {
			continue
		}
		id, slug, ok := sessionOf(rel)
		if !ok {
			continue
		}
		res.Deleted++
		if _, seen := known[id]; seen {
			res.Skipped++
			continue
		}
		if _, seen := l.rows[id]; seen {
			res.Skipped++
			continue
		}
		// The parent of the deleting commit is the last tree that still had the
		// file — deleted in git means absent from the tree, not from history.
		parent := d.Commit + "^"
		head, herr := h.ShowPrefix(ctx, parent, d.Path, headBytes)
		if herr != nil {
			res.Unreadable++
			continue
		}
		when, terr := h.LastCommitTime(ctx, parent, d.Path)
		if terr != nil {
			res.Unreadable++
			continue
		}
		title, cwd := parse(head)
		if l.Note(Entry{
			ID: id, Slug: slug, Title: title, Cwd: cwd,
			End: when.UTC(), Bytes: 0, Seen: time.Now().UTC(),
		}) {
			res.Recovered++
		}
	}
	return res, nil
}

// cliRel strips the staging repo's cli/ prefix, so paths match the form the
// normal sync-time walk records.
func cliRel(p string) (string, bool) {
	if !strings.HasPrefix(p, "cli/projects/") {
		return "", false
	}
	return strings.TrimPrefix(p, "cli/"), true
}

// sessionOf pulls the id and slug out of projects/<slug>/<id>.jsonl, and rejects
// anything deeper — subagent transcripts resolve to their parent's id and would
// overwrite the parent's row with a subagent's first line.
func sessionOf(rel string) (id, slug string, ok bool) {
	if !strings.HasSuffix(rel, ".jsonl") || strings.Count(rel, "/") != 2 {
		return "", "", false
	}
	parts := strings.Split(rel, "/")
	return strings.TrimSuffix(parts[2], ".jsonl"), parts[1], true
}
