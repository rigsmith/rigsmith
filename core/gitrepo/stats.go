package gitrepo

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Stats is a repository's shape and cost: how much history it carries and what
// that history costs on disk.
type Stats struct {
	Commits  int   `json:"commits"`
	Files    int   `json:"files"`    // tracked files at HEAD
	GitBytes int64 `json:"gitBytes"` // .git in total, packed and not
	// LooseBytes is the part of GitBytes sitting in loose objects — written by
	// recent commits and not yet delta-compressed. Split out because it is not
	// the same problem as a long history and does not have the same remedy:
	// append-only transcripts delta superbly, so repacking collapses this while
	// keeping every commit, and pruning history to escape it throws away the
	// wrong thing. It was 82% of a real 2.9 GB repo.
	LooseBytes   int64     `json:"looseBytes"`
	LooseObjects int       `json:"looseObjects"`
	PackBytes    int64     `json:"packBytes"`
	WorkBytes    int64     `json:"workBytes"` // the checkout, i.e. what is actually being kept
	First        time.Time `json:"first"`     // oldest commit still REACHABLE, which is
	// not when the repo started if history has ever been squashed.
	Last   time.Time `json:"last"`
	Branch string    `json:"branch"`
	// RootSubject is the first commit's message. A repo whose root was written by
	// a squash has had everything before it discarded, and reporting First as
	// though it were the beginning turns that loss into a claim that the repo is
	// young. The caller decides how to phrase it; only the caller knows which
	// messages its own squashes write.
	RootSubject string `json:"rootSubject,omitempty"`
}

// Stats gathers the numbers. Local only — nothing here touches the network, so
// it is safe on the status path.
//
// GitBytes against WorkBytes is the ratio worth watching: the checkout is the
// data you are keeping, .git is what it has cost to get here. Transcripts are
// append-only and sync every few minutes, so history outgrows content steadily
// and invisibly, which is the whole reason this needs surfacing at all.
func (r *Repo) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	var err error

	if s.Branch, err = r.CurrentBranch(ctx); err != nil {
		return s, err
	}
	if out, e := runGit(ctx, r.Dir, "rev-list", "--count", "HEAD"); e == nil {
		s.Commits, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if out, e := runGit(ctx, r.Dir, "ls-files"); e == nil {
		if t := strings.TrimSpace(out); t != "" {
			s.Files = strings.Count(t, "\n") + 1
		}
	}
	// --max-parents=0 is the root commit. After a squash that root is a commit
	// the squash itself wrote, so this is where reachable history BEGINS, not
	// when the repo was created — see RootSubject.
	if out, e := runGit(ctx, r.Dir, "rev-list", "--max-parents=0", "--format=%cI%x1f%s", "--no-commit-header", "HEAD"); e == nil {
		iso, subj, _ := strings.Cut(strings.TrimSpace(out), "\x1f")
		s.First = firstTime(iso)
		s.RootSubject = strings.TrimSpace(subj)
	}
	if out, e := runGit(ctx, r.Dir, "log", "-1", "--format=%cI"); e == nil {
		s.Last = firstTime(out)
	}
	s.GitBytes, _ = r.GitDirBytes(ctx)
	s.LooseObjects, s.LooseBytes, s.PackBytes = r.objectCounts(ctx)
	s.WorkBytes, _ = r.WorkTreeBytes(ctx)
	return s, nil
}

// firstTime reads the first parseable RFC3339 line out of git output.
func firstTime(out string) time.Time {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(line)); err == nil {
			return t
		}
	}
	return time.Time{}
}

// objectCounts reads git's own accounting of loose versus packed storage.
// count-objects reports KiB, which is why these are shifted rather than parsed
// as bytes.
func (r *Repo) objectCounts(ctx context.Context) (loose int, looseBytes, packBytes int64) {
	out, err := runGit(ctx, r.Dir, "count-objects", "-v")
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		n, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if perr != nil {
			continue
		}
		switch k {
		case "count":
			loose = int(n)
		case "size":
			looseBytes = n << 10
		case "size-pack":
			packBytes = n << 10
		}
	}
	return loose, looseBytes, packBytes
}

// Repack delta-compresses loose objects into the pack and drops what is
// unreachable. Nothing reachable is lost — this is ordinary git maintenance, and
// it is the remedy to reach for before considering a prune: it costs no history
// at all.
func (r *Repo) Repack(ctx context.Context) error {
	_, err := runGit(ctx, r.Dir, "gc", "--quiet")
	return err
}
