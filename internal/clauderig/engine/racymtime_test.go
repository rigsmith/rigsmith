package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// A transcript rewritten without its mtime moving still has to be staged.
//
// Staging decides a file is current by comparing its mtime to the staged copy's
// — and the staged copy is stamped with the source's own mtime, so equality is
// the whole test. Two different contents can share an mtime whenever both
// writes land inside one filesystem timestamp tick, and then the rewrite is
// invisible: the staged copy keeps the old bytes and nothing reports it.
//
// Chtimes rather than racing a real clock: on a filesystem with fine timestamps
// the two writes get different mtimes and there is nothing to see, so a test
// that waits for a coarse one is a test that passes here and fails in CI —
// which is exactly how it was found.
func TestSync_RewriteUnderTheSameMtimeIsStaged(t *testing.T) {
	live, staging := t.TempDir(), t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rel := "projects/-p/s.jsonl"
	src := filepath.Join(live, filepath.FromSlash(rel))
	// One mtime for both writes, landing on a whole second at the moment of the
	// sync that reads them — which is what a filesystem with a one-second clock
	// produces on its own for two writes this close together.
	stamp := time.Now().Truncate(time.Second)

	writeAt := func(c string) {
		t.Helper()
		write(t, live, rel, strings.Repeat(c, 500)+"\n")
		if err := os.Chtimes(src, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	sync := func() {
		t.Helper()
		if _, err := Sync(Options{
			StagingDir: staging, Config: cliOnlyConfig(live), Machine: m,
			SourceOverride: override("cli", live),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Stand in for a filesystem with a one-second clock. The probe measures the
	// real one, and the machines this runs on tick far finer — so without this
	// the two writes get different stamps and there is nothing to reproduce.
	restore := probeMtimeTick
	probeMtimeTick = func(string) time.Duration { return time.Second }
	t.Cleanup(func() { probeMtimeTick = restore })

	writeAt("a")
	sync()
	writeAt("b") // same size, same mtime, different bytes
	sync()

	got := read(t, filepath.Join(staging, "cli", filepath.FromSlash(rel)))
	if !strings.HasPrefix(got, "b") {
		t.Fatalf("the staged copy still holds the old content (starts %q) — the rewrite was never staged", got[:1])
	}
}

func TestStageClockTrusts(t *testing.T) {
	run := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		clock stageClock
		mod   time.Time
		tick  time.Duration
		want  bool
	}{
		{"no clock yet", stageClock{}, run, time.Second, true},
		// A clock that is there and unreadable is not the same as no clock: it
		// says a run happened and the record of it is gone.
		{"damaged clock", stageClock{suspect: true}, run.Add(-time.Hour), time.Second, false},
		// A fine clock tells two writes apart on its own, so the window is
		// nearly nothing and the incremental path is untouched. This is the
		// ordinary case, and why the fix costs nothing in practice.
		{"fine clock, written just before the run", stageClock{started: run}, run.Add(-time.Millisecond), time.Microsecond, true},
		{"fine clock, inside its own tick", stageClock{started: run}, run, time.Microsecond, false},
		{"coarse clock, long before the run", stageClock{started: run}, run.Add(-time.Hour), time.Second, true},
		// Same tick as the run that staged it: a later write in that tick
		// reuses this mtime, so the staged copy cannot be told apart.
		{"coarse clock, same tick as the run", stageClock{started: run}, run, time.Second, false},
		{"coarse clock, just inside the window", stageClock{started: run}, run.Add(-time.Second / 2), time.Second, false},
		{"coarse clock, just outside the window", stageClock{started: run}, run.Add(-2 * time.Second), time.Second, true},
	}
	for _, c := range cases {
		if got := c.clock.trusts(c.mod, c.tick); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Damage is not absence: a clock that cannot be read says a run happened and
// its record is gone, which is not a reason to believe the mtimes it would have
// vouched for.
func TestStageClockRoundTrips(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if c := readStageClock(staging); !c.started.IsZero() || c.suspect {
		t.Errorf("an absent clock read as %+v, want no clock and no suspicion", c)
	}
	want := time.Now()
	writeStageClock(staging, want)
	if got := readStageClock(staging); !got.started.Equal(want) || got.suspect {
		t.Errorf("got %+v, want %v", got, want)
	}
	if err := os.WriteFile(stageClockPath(staging), []byte("nonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readStageClock(staging)
	if !got.suspect {
		t.Fatalf("a corrupt clock read as %+v, want suspect", got)
	}
	if got.trusts(time.Now().Add(-time.Hour), time.Second) {
		t.Error("a corrupt clock still trusted an mtime")
	}
	// A completed run puts it right.
	writeStageClock(staging, want)
	if c := readStageClock(staging); c.suspect {
		t.Error("a completed run did not clear the suspicion")
	}
}

// The probe has to find a fine clock where there is one, or every sync widens
// its window for a hazard the filesystem does not have.
func TestProbeMtimeTick(t *testing.T) {
	got := probeMtimeTick(t.TempDir())
	if got <= 0 || got > coarseTick {
		t.Errorf("tick = %v, want a positive measurement no coarser than %v", got, coarseTick)
	}
	if got == coarseTick {
		t.Logf("this filesystem records whole seconds (tick %v) — the slow path is correct here", got)
	}
	// Nowhere to write is no reason to assume the best.
	if got := probeMtimeTick(filepath.Join(t.TempDir(), "no", "such", "dir")); got != coarseTick {
		t.Errorf("unusable directory gave %v, want the coarse assumption %v", got, coarseTick)
	}
}

// settle backdates fixture files so their mtimes are unambiguously older than
// the run that reads them — what "not touched since the last sync" looks like.
// Fixtures written moments before a sync sit inside the filesystem's own tick,
// where sync cannot tell them from a file rewritten during that tick and
// restages to be safe.
func settle(t *testing.T, root string, rels ...string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, rel := range rels {
		if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), old, old); err != nil {
			t.Fatal(err)
		}
	}
}
