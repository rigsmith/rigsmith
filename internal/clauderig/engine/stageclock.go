package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The staging clock: when the last completed sync began.
//
// Staging decides a source file is already current by comparing its mtime to
// the staged copy's, and the staged copy is stamped with the source's own mtime
// — so equality is the entire test. That holds only while an mtime identifies a
// version of the file, and it stops holding when two writes land inside one
// filesystem timestamp tick. The second one is then invisible: the staged copy
// keeps the first one's bytes, no error is raised, and the transcript in the
// repo is quietly wrong from then on.
//
// Ticks are not always small. A runner's overlay filesystem was coarse enough
// to do this to two writes milliseconds apart, which is how it was found.
//
// This is git's racy-index problem and it takes git's answer: remember when the
// run began, and refuse to trust an mtime at or after that instant, because a
// file written in the same tick as the copy cannot be told from one written
// before it. Such a file is treated as changed — which for almost everything
// means staged again, and restaging identical bytes writes the same file, so
// git sees nothing and being wrong in this direction costs a copy. A large
// transcript still meets the growth throttle above it and may wait for a chunk
// or for the session to go quiet, which is that rule doing its job: the restage
// is delayed, not skipped.
//
// One instant for the whole run, not one per file: the question is only whether
// a file could have changed around the time the run was reading, and the start
// of the run is the conservative answer to that for every file in it.
const stageClockName = ".stage-clock"

func stageClockPath(staging string) string {
	if staging == "" {
		return ""
	}
	// Beside the staging tree, never inside it: this describes what THIS
	// machine last did, and everything in the tree is committed and shared.
	return filepath.Join(filepath.Dir(staging), stageClockName)
}

// readStageClock reports when the last completed sync began, or the zero time
// when that is not known.
//
// Absent reads as unknown, and unknown trusts the mtime — the behaviour before
// this file existed. The alternative is to distrust every mtime on the first
// run after an upgrade and restage the entire tree to learn nothing, since
// there is no evidence yet that anything is wrong.
func readStageClock(staging string) stageClock {
	p := stageClockPath(staging)
	if p == "" {
		return stageClock{}
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return stageClock{} // no run has finished here yet
	}
	if err != nil {
		return stageClock{suspect: true}
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || ns <= 0 {
		// Something wrote this and it is not what we write. Not knowing when
		// the last run was is only safe while there is no reason to think one
		// happened, and a file sitting here is that reason.
		return stageClock{suspect: true}
	}
	return stageClock{started: time.Unix(0, ns)}
}

// stageClock is what the last completed run left behind: when it began, and
// whether that answer can be believed.
type stageClock struct {
	// started is zero when no clock is there at all — a first run, or a
	// staging tree from before this existed.
	started time.Time
	// suspect is a clock that exists and could not be read. Absence says
	// nothing happened; damage says something did and the record of it is
	// gone, which is not the same and is not safe to treat as the same.
	suspect bool
}

// trusts reports whether a source mtime identifies the file's contents: older
// than the last run by more than one tick of the clock that stamped it, so no
// tick it could share reaches into that run.
func (c stageClock) trusts(mod time.Time, tick time.Duration) bool {
	switch {
	case c.suspect:
		return false
	case c.started.IsZero():
		return true
	}
	return mod.Before(c.started.Add(-tick))
}

// writeStageClock records when this run began.
//
// Written whole or not at all, the same way the audit cache is: writing in
// place truncates first, and an interruption there leaves a clock that reads as
// damaged — which then distrusts every mtime and restages the tree. Correct,
// but a needless day's work for anyone it happened to.
//
// Best-effort past that: a clock that cannot be written costs the next run some
// restaging, which is the harmless direction.
func writeStageClock(staging string, started time.Time) {
	p := stageClockPath(staging)
	if p == "" {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".stage-clock-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strconv.FormatInt(started.UnixNano(), 10) + "\n"); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), p)
}

// coarseTick is what a filesystem that records only whole seconds is assumed to
// have, and the ceiling on any measurement. ext3 ticks at a second and FAT at
// two; measuring cannot tell them apart from here, and a second of slack
// already covers the case this exists for.
const coarseTick = time.Second

// probeMtimeTick measures how finely dir's filesystem records modification
// times, which is the width of the window above.
//
// Measured rather than inferred from the files themselves. A round mtime is not
// evidence of a coarse clock — archives, restores and anything else that stamps
// times explicitly produce whole seconds on a filesystem that records far more,
// and one restore here stamped 541 transcripts with the same minute. Nor is a
// non-zero nanosecond field evidence of a fine one: Linux fills those in from a
// clock it only updates each kernel tick, so two writes milliseconds apart can
// carry the same stamp down to the nanosecond. That is the case this whole file
// exists for, and the case reading the digits would miss.
//
// The smallest gap two writes can be told apart by is the answer, so it takes
// the smallest non-zero difference it sees. Stamps that never differ mean a
// clock coarser than this loop can measure.
var probeMtimeTick = func(dir string) time.Duration {
	f, err := os.CreateTemp(dir, ".tick-*")
	if err != nil {
		return coarseTick // no way to look is not a reason to assume the best
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)

	stamp := func() (time.Time, bool) {
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			return time.Time{}, false
		}
		fi, err := os.Stat(name)
		if err != nil {
			return time.Time{}, false
		}
		return fi.ModTime(), true
	}
	best := time.Duration(0)
	prev, ok := stamp()
	if !ok {
		return coarseTick
	}
	for i := 0; i < 8; i++ {
		next, ok := stamp()
		if !ok {
			return coarseTick
		}
		if d := next.Sub(prev); d > 0 && (best == 0 || d < best) {
			best = d
		}
		prev = next
	}
	if best <= 0 || best > coarseTick {
		return coarseTick
	}
	return best
}
