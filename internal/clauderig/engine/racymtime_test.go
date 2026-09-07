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
	// real one, and every machine this test runs on records nanoseconds — so
	// without this there is nothing to reproduce.
	restore := probeMtimeGranularity
	probeMtimeGranularity = func(string) bool { return true }
	t.Cleanup(func() { probeMtimeGranularity = restore })

	writeAt("a")
	sync()
	writeAt("b") // same size, same mtime, different bytes
	sync()

	got := read(t, filepath.Join(staging, "cli", filepath.FromSlash(rel)))
	if !strings.HasPrefix(got, "b") {
		t.Fatalf("the staged copy still holds the old content (starts %q) — the rewrite was never staged", got[:1])
	}
}

func TestMtimeIsTrustworthy(t *testing.T) {
	run := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mod    time.Time
		last   time.Time
		coarse bool
		want   bool
	}{
		{"nothing known yet", run, time.Time{}, true, true},
		// The common case, and the reason this costs nothing in practice.
		{"filesystem records nanoseconds", run, run, false, true},
		{"coarse, long before the run", run.Add(-time.Hour), run, true, true},
		// Same second as the run that staged it: a later write in that second
		// reuses this mtime, so the staged copy cannot be told apart.
		{"coarse, same tick as the run", run, run, true, false},
		{"coarse, one second before", run.Add(-time.Second), run, true, false},
		{"coarse, just outside the window", run.Add(-3 * time.Second), run, true, true},
	}
	for _, c := range cases {
		if got := mtimeIsTrustworthy(c.mod, c.last, c.coarse); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The clock has to survive the trip through the file, or every run distrusts
// every coarse mtime and restages the tree.
func TestStageClockRoundTrips(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	if readStageClock(staging).IsZero() != true {
		t.Error("an absent clock did not read as unknown")
	}
	want := time.Now().Truncate(time.Nanosecond)
	writeStageClock(staging, want)
	if got := readStageClock(staging); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Anything unreadable reads as unknown, which trusts the mtime — the
	// behaviour before the clock existed.
	if err := os.WriteFile(stageClockPath(staging), []byte("nonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !readStageClock(staging).IsZero() {
		t.Error("a corrupt clock was believed")
	}
}

// The probe has to say "fine" on the machines this actually runs on, or every
// sync takes the slow path for a hazard it does not have.
func TestProbeMtimeGranularity(t *testing.T) {
	if probeMtimeGranularity(t.TempDir()) {
		t.Error("this filesystem was reported as second-granular; if that is really true, say so here")
	}
	// Nowhere to write is not evidence of anything, and reads as fine-grained
	// so the fast path survives.
	if probeMtimeGranularity(filepath.Join(t.TempDir(), "no", "such", "dir")) {
		t.Error("an unusable directory was reported as coarse")
	}
}
