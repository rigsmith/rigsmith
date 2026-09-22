package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// plan builds resolved steps in order; a name in skipped carries --from's
// skip reason.
func plan(names []string, skipped ...string) []pipeline.ResolvedStep {
	skip := map[string]bool{}
	for _, s := range skipped {
		skip[s] = true
	}
	steps := make([]pipeline.ResolvedStep, len(names))
	for i, n := range names {
		steps[i] = pipeline.ResolvedStep{Name: n}
		if skip[n] {
			steps[i].SkipReason = pipeline.BeforeFromSkipReason
		}
	}
	return steps
}

var order = []string{"version", "commit", "build", "publish", "tag", "push"}

func TestNeverRan(t *testing.T) {
	// The #420 resume: stopped at commit, then --from publish.
	steps := plan(order, "version", "commit", "build")
	st := &resumeState{Next: "commit"}

	if got, want := neverRan(steps, st, "publish"), []string{"commit", "build"}; !reflect.DeepEqual(got, want) {
		t.Errorf("neverRan = %v, want %v (version completed before the stop, so it is not listed)", got, want)
	}
	if got := neverRan(plan(order, "version"), st, "commit"); got != nil {
		t.Errorf("resuming at the stopped step skips nothing that never ran, got %v", got)
	}
	if got := neverRan(steps, nil, "publish"); got != nil {
		t.Errorf("no recorded state, no guard; got %v", got)
	}
	if got := neverRan(steps, &resumeState{Next: "gone"}, "publish"); got != nil {
		t.Errorf("a recorded step the config no longer has cannot be compared; got %v", got)
	}
}

func TestRecordResumeState(t *testing.T) {
	steps := plan(order)
	path := filepath.Join(t.TempDir(), resumeStateFile)
	next := func() string {
		if st := readResumeState(path); st != nil {
			return st.Next
		}
		return ""
	}
	record := func(ok bool, stoppedAt, to string, narrowed bool) {
		t.Helper()
		if err := recordResumeState(path, steps, ok, stoppedAt, to, narrowed); err != nil {
			t.Fatal(err)
		}
	}

	record(false, "commit", "", false)
	if next() != "commit" {
		t.Fatalf("a failure at commit: next = %q, want commit", next())
	}

	record(false, "", "", false)
	if next() != "commit" {
		t.Errorf("a stop before any step moves nothing: next = %q, want commit", next())
	}

	// --only publish succeeds: it left commit and build out on purpose, so
	// the release is still unfinished from commit.
	record(true, "", "", true)
	if next() != "commit" {
		t.Errorf("a narrowed success must not clear the state: next = %q, want commit", next())
	}
	// ...and a narrowed failure later in the order must not move it later.
	record(false, "publish", "", true)
	if next() != "commit" {
		t.Errorf("a narrowed failure moved the point later: next = %q, want commit", next())
	}

	record(true, "", "build", false)
	if next() != "publish" {
		t.Errorf("a clean run to --to build continues at publish: next = %q", next())
	}

	record(true, "", "", false)
	if next() != "" {
		t.Errorf("a complete run clears the state, got next = %q", next())
	}

	if err := recordResumeState("", steps, false, "commit", "", false); err != nil {
		t.Errorf("outside git (no path) recording is a no-op, got %v", err)
	}
}
