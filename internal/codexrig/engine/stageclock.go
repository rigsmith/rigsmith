package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The incremental skip trusts source-mtime equality: a staged copy whose mtime
// matches the source was staged from this version of this file. That reasoning
// has one hole, and it is git's oldest one — two writes inside a single
// filesystem mtime tick are indistinguishable, so the second is invisible.
//
// The answer, taken from git: record when the run STARTED, and trust an mtime
// only once it is safely older than that instant. A file written during the run
// is not trusted until the next one, which costs one extra copy and never a
// silent loss.
//
// The clock lives beside the staging repo rather than inside it, because it
// describes what THIS machine has staged and everything in the tree is shared.

const stageClockName = ".stage-clock"

type stageClock struct {
	started time.Time
	// suspect means a clock was found and could not be read. It trusts nothing,
	// which restages the tree — the harmless direction. A missing clock (a fresh
	// staging dir) trusts everything, because there is nothing staged to doubt.
	suspect bool
}

func stageClockPath(staging string) string {
	if staging == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(staging), stageClockName)
}

func readStageClock(staging string) stageClock {
	p := stageClockPath(staging)
	if p == "" {
		return stageClock{}
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return stageClock{}
	}
	if err != nil {
		return stageClock{suspect: true}
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || ns <= 0 {
		return stageClock{suspect: true}
	}
	return stageClock{started: time.Unix(0, ns)}
}

// writeStageClock records when this run began, whole-file via a temp and a
// rename. Truncating in place would leave a half-written clock on a crash, which
// reads as suspect and restages the whole tree.
func writeStageClock(staging string, started time.Time) {
	p := stageClockPath(staging)
	if p == "" {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(started.UnixNano(), 10)+"\n"), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// trusts reports whether a source mtime is old enough to be evidence of the
// file's contents, given the filesystem's mtime granularity.
func (c stageClock) trusts(mod time.Time, tick time.Duration) bool {
	if c.suspect {
		return false
	}
	if c.started.IsZero() {
		return true
	}
	return mod.Before(c.started.Add(-tick))
}

// coarseTick caps the measured granularity. A filesystem that reports worse than
// a second is either lying or so slow that a second of extra copying is not the
// problem.
const coarseTick = time.Second

// probeMtimeTick measures a filesystem's mtime granularity by writing and
// statting. It measures the SOURCE filesystem, not the staging one: the mtimes
// being judged come from the source, and a root on a network share can be far
// coarser than the local disk.
func probeMtimeTick(dir string) time.Duration {
	f, err := os.CreateTemp(dir, ".codexrig-tick-*")
	if err != nil {
		return coarseTick
	}
	name := f.Name()
	defer func() {
		f.Close()
		_ = os.Remove(name)
	}()

	best := time.Duration(0)
	var prev time.Time
	for i := 0; i < 8; i++ {
		if _, err := f.WriteString("x"); err != nil {
			return coarseTick
		}
		if err := f.Sync(); err != nil {
			return coarseTick
		}
		st, err := f.Stat()
		if err != nil {
			return coarseTick
		}
		if !prev.IsZero() {
			if d := st.ModTime().Sub(prev); d > 0 && (best == 0 || d < best) {
				best = d
			}
		}
		prev = st.ModTime()
	}
	if best <= 0 || best > coarseTick {
		return coarseTick
	}
	return best
}
