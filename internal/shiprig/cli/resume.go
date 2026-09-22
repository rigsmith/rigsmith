package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// A release that stops partway leaves work undone, and `--from` lets the next
// run start anywhere. Starting past the step it stopped at silently skips
// steps whose output later ones consume: resuming "from publish" after a
// failure at commit publishes packages `build` never built (#420). So shiprig
// records where an unfinished release got to, and refuses a `--from` past it
// unless forced.

// resumeStateFile lives in the git directory, never the work tree: the commit
// step stages everything, and this must not ride along into a release commit.
const resumeStateFile = "shiprig-release.json"

// resumeState is where the unfinished release got to.
type resumeState struct {
	// Next is the first step that has not completed: where a resume has to
	// start, or earlier.
	Next string `json:"next"`
}

// resumeStatePath returns the state file's path, or "" outside a git
// repository (there is then no state, and no guard).
func resumeStatePath(ctx context.Context, root string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}
	return filepath.Join(strings.TrimSpace(string(out)), resumeStateFile)
}

func readResumeState(path string) *resumeState {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st resumeState
	if json.Unmarshal(data, &st) != nil || st.Next == "" {
		return nil
	}
	return &st
}

// recordResumeState updates the state after a real run: removed once nothing
// is left to do, otherwise pointing at the first step still to run. A run
// that stops before its first step (a global hook, a variable) leaves the
// state as it was: it moved nothing forward. So does most of a run narrowed by
// --only/--skip: it left steps out on purpose, so it may move the resume
// point earlier (it failed sooner) but never later, and never clears it.
func recordResumeState(path string, steps []pipeline.ResolvedStep, ok bool, stoppedAt, to string, narrowed bool) error {
	if path == "" {
		return nil
	}
	next := ""
	switch {
	case !ok && stoppedAt == "":
		return nil
	case !ok:
		next = stoppedAt
	case to != "":
		// A clean run that stopped at --to: the release continues after it.
		for i, s := range steps {
			if s.Name == to && i+1 < len(steps) {
				next = steps[i+1].Name
			}
		}
	}
	if narrowed {
		if prev := readResumeState(path); prev != nil && (next == "" || indexOf(steps, prev.Next) < indexOf(steps, next)) {
			next = prev.Next
		} else if prev == nil && next == "" {
			return nil
		}
	}
	if next == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(resumeState{Next: next})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// indexOf is name's position in steps, or len(steps) when absent (a step the
// config no longer has sorts last, so it never wins as the earlier point).
func indexOf(steps []pipeline.ResolvedStep, name string) int {
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	return len(steps)
}

// skippedByFrom lists the steps a --from resume leaves out that would
// otherwise have run, in order.
func skippedByFrom(steps []pipeline.ResolvedStep) []string {
	var names []string
	for _, s := range steps {
		if s.SkipReason == pipeline.BeforeFromSkipReason {
			names = append(names, s.Name)
		}
	}
	return names
}

// neverRan lists the skipped steps from the unfinished release's next step
// onward: the ones --from skips although that release never completed them.
// Empty when there is no state, or --from starts at or before its next step.
func neverRan(steps []pipeline.ResolvedStep, st *resumeState, from string) []string {
	if st == nil || from == "" {
		return nil
	}
	nextAt, fromAt := -1, -1
	for i, s := range steps {
		switch s.Name {
		case st.Next:
			nextAt = i
		case from:
			fromAt = i
		}
	}
	if nextAt < 0 || fromAt <= nextAt {
		return nil
	}
	var names []string
	for _, s := range steps[nextAt:fromAt] {
		if s.SkipReason == pipeline.BeforeFromSkipReason {
			names = append(names, s.Name)
		}
	}
	return names
}

// resumeGuardError explains a refused --from.
func resumeGuardError(tool, from, next string, skipped []string) error {
	return fmt.Errorf("the last release stopped at '%s', so --from %s would skip %s, which never ran in it — "+
		"steps a later one may depend on (publish ships what build produced). "+
		"Resume with `%s release --from %s`, or pass --force to skip them anyway",
		next, from, strings.Join(skipped, ", "), tool, next)
}
