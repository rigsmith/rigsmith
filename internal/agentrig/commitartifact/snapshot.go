package commitartifact

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

const snapshotVisitLimit = 512

// SnapshotTime returns the origin time of the current conflict side's bytes.
// Unchanged commits and merges that copied a parent do not refresh that time.
// A merge with novel bytes has no provable single snapshot origin and is refused.
// If identical bytes came from multiple parents, the latest proven origin wins.
// The timestamp is Git's recorded committer time, not a trusted wall clock.
func (f *relatedFiles) SnapshotTime(ctx context.Context, side ConflictSide) (stamp time.Time, err error) {
	defer func() {
		if err != nil {
			f.err = err
		}
	}()
	if err = f.begin(ctx); err != nil {
		return time.Time{}, err
	}
	if side != OurSide && side != TheirSide {
		return time.Time{}, ErrInvalid
	}
	mode, oid, err := f.repo.relatedEntry(ctx, f.sides[side], f.owner.path)
	if err != nil {
		return time.Time{}, err
	}
	if mode != f.owner.mode || oid != f.owner.oids[side] {
		return time.Time{}, ErrConflict
	}
	return f.snapshotOrigin(ctx, f.sides[side], mode, oid)
}

func (f *relatedFiles) snapshotOrigin(ctx context.Context, commit, mode, oid string) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	key := commit + "\x00" + f.owner.path
	if stamp, ok := f.origins[key]; ok {
		return stamp, nil
	}
	f.originVisits++
	if f.originVisits > snapshotVisitLimit {
		return time.Time{}, artifact.ErrTooLarge
	}
	raw, err := f.repo.run(ctx, nil, "show", "-s", "--format=%ct%n%P", commit)
	if err != nil {
		return time.Time{}, err
	}
	seconds, rest, ok := strings.Cut(raw, "\n")
	sec, err := strconv.ParseInt(seconds, 10, 64)
	if !ok || err != nil || sec < 0 || sec > 253402300799 {
		return time.Time{}, ErrInvalid
	}
	parents := strings.Fields(rest)
	if len(parents) > 8 {
		return time.Time{}, artifact.ErrTooLarge
	}
	var matched []string
	for _, parent := range parents {
		if !objectID(parent) {
			return time.Time{}, ErrInvalid
		}
		pm, po, err := f.repo.relatedEntry(ctx, parent, f.owner.path)
		if err != nil {
			return time.Time{}, err
		}
		if pm == mode && po == oid {
			matched = append(matched, parent)
		}
	}
	stamp := time.Unix(sec, 0).UTC()
	if len(matched) == 0 {
		if len(parents) > 1 {
			return time.Time{}, ErrConflict
		}
	} else {
		stamp = time.Time{}
		for _, parent := range matched {
			candidate, err := f.snapshotOrigin(ctx, parent, mode, oid)
			if err != nil {
				return time.Time{}, err
			}
			if stamp.IsZero() || candidate.After(stamp) {
				stamp = candidate
			}
		}
	}
	if f.origins == nil {
		f.origins = map[string]time.Time{}
	}
	f.origins[key] = stamp
	return stamp, nil
}
