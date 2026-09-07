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
// before it. Such a file is simply staged again — restaging identical bytes
// writes the same file and git sees no change, so being wrong in this direction
// costs a copy and nothing else.
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
func readStageClock(staging string) time.Time {
	p := stageClockPath(staging)
	if p == "" {
		return time.Time{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// writeStageClock records when this run began. Best-effort: a clock that cannot
// be written costs the next run some restaging, which is the harmless
// direction.
func writeStageClock(staging string, started time.Time) {
	p := stageClockPath(staging)
	if p == "" {
		return
	}
	_ = os.WriteFile(p, []byte(strconv.FormatInt(started.UnixNano(), 10)+"\n"), 0o600)
}

// stageClockSlack is how much older than the run an mtime has to be before it
// is taken as evidence.
//
// The hazard is a file whose mtime lands in the same tick as the run that
// staged it: a later write inside that tick reuses the mtime the staged copy
// was stamped with, so the two versions are indistinguishable. Being *before*
// the run is not enough, since a tick spans both.
//
// Two seconds because the granularity is not knowable from here and 2s is the
// coarsest in ordinary use (FAT); one second covers ext3 and the sub-second
// filesystems need none of it. The cost of overshooting is that a file written
// within two seconds of a sync is staged once more than it had to be.
const stageClockSlack = 2 * time.Second

// mtimeIsTrustworthy reports whether a source mtime identifies the file's
// contents.
//
// On a filesystem that records sub-second times the tick is nanoseconds, no
// realistic pair of writes shares one, and the mtime is taken at face value —
// the incremental path is untouched. That is nearly every machine, which is why
// this costs nothing in ordinary use.
//
// Where the tick is a whole second, the mtime has to be older than the last run
// by more than a tick could span. A zero clock means nothing is known, and
// nothing known trusts the mtime.
func mtimeIsTrustworthy(mod, lastRunStarted time.Time, coarse bool) bool {
	if lastRunStarted.IsZero() || !coarse {
		return true
	}
	return mod.Before(lastRunStarted.Add(-stageClockSlack))
}

// probeMtimeGranularity reports whether dir's filesystem records only whole
// seconds, which is the condition the rule above exists for.
//
// Measured rather than inferred from the files themselves. A round mtime is not
// evidence of a coarse clock: archives, restores and anything else that stamps
// times explicitly produce whole seconds on a filesystem that would have
// recorded nanoseconds — one restore here stamped 541 transcripts with the same
// minute. Inferring from those would put every one of them on the slow path
// forever, for a hazard they do not have.
//
// Unknowable reads as fine-grained, which keeps the fast path and the behaviour
// that was there before this: a probe that cannot run is not evidence either.
var probeMtimeGranularity = func(dir string) (coarse bool) {
	f, err := os.CreateTemp(dir, ".tick-*")
	if err != nil {
		return false
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString("x"); err != nil {
		f.Close()
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	fi, err := os.Stat(name)
	if err != nil {
		return false
	}
	// A filesystem storing only seconds has nothing else to report. One that
	// stores more can still land on a whole second by chance, which reads as
	// coarse for this run and costs some restaging — the harmless direction,
	// and about a billion to one.
	return fi.ModTime().Nanosecond() == 0
}
