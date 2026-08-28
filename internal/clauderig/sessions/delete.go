package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// Deletion reports what removing a session actually did. Partial outcomes are
// normal — a sidecar can be gone already, a staged copy can be read-only — and
// each is named rather than folded into one success/failure, because "deleted"
// while one copy survived is the answer that costs someone their assumption.
type Deletion struct {
	Removed []string
	Failed  []DeleteFailure
	// Remaining names the stores that still hold a copy after this ran, whether
	// because they were not selected or because removal failed.
	Remaining []string
}

// DeleteFailure is one path that could not be removed, and why.
type DeleteFailure struct {
	Path   string
	Reason string
}

// ErrSessionLive is returned when the session is being written to right now.
// Deleting the transcript under a running Claude Code session loses the turns
// it has not flushed and leaves the process writing to an unlinked file — the
// same hazard restore's live-session guard exists to prevent.
type ErrSessionLive struct {
	PID int
	Cwd string
}

func (e *ErrSessionLive) Error() string {
	if e.Cwd != "" {
		return fmt.Sprintf("that session is running right now (pid %d, %s) — close it first", e.PID, e.Cwd)
	}
	return fmt.Sprintf("that session is running right now (pid %d) — close it first", e.PID)
}

// Delete removes a session's files from the named stores only ([CLISource],
// [DesktopSource], [RepoSource]), leaving every other copy alone.
//
// Per-store rather than all-or-nothing because the stores mean different
// things: dropping the local copy of a synced session frees space and the next
// restore brings it back, while dropping the synced copy propagates to every
// other machine on the next sync. Those are not the same decision and the
// caller has to make it explicitly.
//
// The ledger row is deliberately NOT removed. It is the permanent index of what
// existed, it carries no conversation content, and a session whose body is gone
// still listing as "gone" is the honest record — silently forgetting that a
// session ever existed is how the divergence went unnoticed in the first place.
//
// claudeHome is the live ~/.claude, used for the running-session check; pass ""
// to skip it (there is nothing live to protect in a synced-only tree).
func Delete(row Row, stores []string, claudeHome string) (Deletion, error) {
	var d Deletion
	want := map[string]bool{}
	for _, s := range stores {
		want[s] = true
	}
	if len(want) == 0 {
		return d, fmt.Errorf("no stores named — nothing would be deleted")
	}

	// A live session is refused outright rather than partially honoured: if the
	// process is still appending, no part of this is safe to do.
	if claudeHome != "" && (want[CLISource] || want[DesktopSource]) {
		for _, inst := range account.RunningInstances(claudeHome) {
			if inst.SessionID != "" && session.CanonicalID(inst.SessionID) == session.CanonicalID(row.ID) {
				return d, &ErrSessionLive{PID: inst.PID, Cwd: inst.Cwd}
			}
		}
	}

	for _, store := range []string{CLISource, DesktopSource, RepoSource} {
		if !want[store] {
			if len(pathsFor(row, store)) > 0 {
				d.Remaining = appendOnce(d.Remaining, store)
			}
			continue
		}
		for _, p := range pathsFor(row, store) {
			if err := removeSessionPath(p, row.ID); err != nil {
				d.Failed = append(d.Failed, DeleteFailure{Path: p, Reason: err.Error()})
				d.Remaining = appendOnce(d.Remaining, store)
				continue
			}
			d.Removed = append(d.Removed, p)
		}
	}
	return d, nil
}

// pathsFor lists every file this store holds for the session: the transcript,
// and for the Desktop stores the sidecars filed alongside it.
func pathsFor(row Row, store string) []string {
	var out []string
	if p := row.Paths[store]; p != "" {
		out = append(out, p)
	}
	for _, sc := range row.Meta.Sidecars {
		// A sidecar scanned under "repo" belongs to the synced copy even though
		// it is a Desktop file; the label records where it was found, which is
		// exactly the question here.
		if sc.Label == store && sc.Path != "" {
			out = append(out, sc.Path)
		}
	}
	return out
}

// removeSessionPath deletes one file belonging to this session, refusing
// anything whose name does not identify it.
//
// The guard is not defending against a hostile caller — the paths come from our
// own scan. It is defending against a future change to that scan handing this
// function a directory root, which would be unrecoverable and silent. A delete
// gets a belt as well as braces.
func removeSessionPath(path, id string) error {
	id = session.CanonicalID(id)
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == id+".jsonl", base == "local_"+id+".json":
	default:
		return fmt.Errorf("refusing to delete %q: its name does not identify session %s", filepath.Base(path), id)
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone is the state we wanted
		}
		return err
	}
	// Sub-agent transcripts live in a sibling directory named for the session.
	// Removed only after the transcript itself, so a failure there never
	// orphans the directory.
	if strings.HasSuffix(base, ".jsonl") {
		sub := filepath.Join(filepath.Dir(path), filepath.Base(path[:len(path)-len(".jsonl")]))
		if info, err := os.Stat(sub); err == nil && info.IsDir() {
			_ = os.RemoveAll(sub)
		}
	}
	return nil
}

func appendOnce(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
