package gitrepo

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// FileChange is one path in a commit, with the line counts git reports for it.
// Added and Removed are -1 for a binary file, which is what --numstat writes as
// "-" and what "no line count applies" has to look like to a caller.
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// CommitAt finds the commit a record written at t belongs to: the earliest one
// with the given subject at or after t.
//
// Matching on time rather than on an id recorded in the record, because there is
// no id to record — the journal line is written BEFORE the commit precisely so
// it travels inside it, which means the sha does not exist yet at the moment the
// record is made. The commit therefore always lands a moment after its own
// record, never before, and "the first one at or after" is exact.
//
// ok is false when nothing matches, which is the ordinary state of a record
// whose sync has not committed yet.
//
// The window is opened a second early and then closed again on the commit's own
// timestamp. Git records commit times to the second while a journal record
// carries fractions, so a commit made 300ms after its record reads as EARLIER
// than it — the slack is what stops that one being missed, and comparing
// against the record's second is what stops a genuinely older commit with the
// same subject being taken instead.
func (r *Repo) CommitAt(ctx context.Context, t time.Time, subject string) (sha string, ok bool, err error) {
	// --reverse, so the first match is the earliest commit at or after t. Not
	// --max-count: git applies the cap before the ordering, so a cap here would
	// keep the NEWEST commits since t and drop the very one being looked for
	// whenever a record is more than a few hundred syncs old.
	out, err := runGit(ctx, r.Dir,
		"log", "--since="+t.Add(-time.Second).Format(time.RFC3339),
		"--reverse", "--format=%H%x1f%cI%x1f%s")
	if err != nil {
		return "", false, err
	}
	floor := t.Truncate(time.Second)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		h, rest, found := strings.Cut(strings.TrimSpace(line), "\x1f")
		if !found {
			continue
		}
		iso, subj, found := strings.Cut(rest, "\x1f")
		if !found || subj != subject {
			continue
		}
		at, perr := time.Parse(time.RFC3339, iso)
		if perr != nil || at.Before(floor) {
			continue
		}
		return h, true, nil
	}
	return "", false, nil
}

// CommitFiles lists what a commit touched. The counts come from --numstat, which
// unlike --stat is not truncated or scaled for display.
func (r *Repo) CommitFiles(ctx context.Context, sha string) ([]FileChange, error) {
	// --format= empties the header so only the numstat body comes back, and
	// --no-renames keeps a moved file as one path rather than an arrow.
	out, err := runGit(ctx, r.Dir, "show", "--numstat", "--no-renames", "--format=", sha)
	if err != nil {
		return nil, err
	}
	var files []FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		files = append(files, FileChange{
			Path:    parts[2],
			Added:   numstat(parts[0]),
			Removed: numstat(parts[1]),
		})
	}
	return files, nil
}

// numstat reads one --numstat column. "-" means binary, which has no line count.
func numstat(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
