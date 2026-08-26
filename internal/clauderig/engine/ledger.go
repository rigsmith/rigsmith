package engine

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// recordLedger walks the STAGED transcript tree and records every session it
// finds into this device's ledger.
//
// Staged, not live, and deliberately: staging holds this machine's transcripts
// plus every other machine's and every earlier sync's, so one walk covers all of
// them. It runs BEFORE the retention prune, which is the whole point — the last
// moment a soon-to-be-dropped transcript can still be read for its title and
// project.
//
// Rows already matching the transcript's size and mtime are skipped without
// opening the file, so a steady-state sync reads nothing and writes nothing.
func recordLedger(stagingDir, device, liveAccount string, mine map[string]bool) (added int, total int, err error) {
	l, err := ledger.Open(stagingDir, device)
	if err != nil {
		return 0, 0, err
	}
	// Ground truth first: Desktop files each session under the account that
	// opened it, so anything this covers needs no guessing. It covers only
	// sessions opened through Desktop (3% of a real staged tree), which is why
	// liveAccount exists as the fallback for the rest.
	byDesktop, contested := desktopSessionAccounts(stagingDir)
	// Judge "would this attribution improve things?" against EVERY device's
	// ledger, not just this one's. Another machine may already hold Desktop
	// ground truth for a session this machine only has a transcript for, and
	// writing a weaker guess here would churn a row every sync for an answer
	// the union then discards anyway.
	union := ledger.LoadAll(stagingDir)
	projects := filepath.Join(stagingDir, "cli", "projects")
	if dirExists(projects) {
		walkErr := filepath.WalkDir(projects, func(p string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			rel, rerr := filepath.Rel(filepath.Join(stagingDir, "cli"), p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			// Only the session's own transcript, projects/<slug>/<id>.jsonl. Deeper
			// files (subagents/, tool-results/) resolve to the SAME session id, so
			// recording them would have every subagent overwrite the parent's row —
			// measured on a real tree: 822 files collapsing to 574 sessions, and a
			// steady-state pass still rewriting 369 rows because each pass let a
			// different file win.
			if strings.Count(rel, "/") != 2 {
				return nil
			}
			id := session.IDFromTranscriptRel(rel)
			if id == "" {
				return nil
			}
			var acct, src string
			// A contested session is not merely unattributed: whatever was
			// recorded before the conflict outranks every later answer, so it
			// has to be revoked explicitly or it filters forever under an
			// account that is now disputed.
			if contested[id] {
				if l.Revoke(id) {
					added++
				}
				return nil
			}
			if a := byDesktop[id]; a != "" {
				acct, src = a, ledger.AccountFromDesktop
			} else if liveAccount != "" && mine[id] {
				// Only for sessions THIS machine's own root offered. The walk
				// above covers the shared staged tree, which holds every
				// machine's transcripts, so attributing on presence there would
				// have the first machine to sync claim sessions it never ran —
				// and sticky attribution would make that permanent.
				//
				// Residual, stated rather than hidden: a machine that RESTORED
				// another's transcripts now has them in its own root, so if the
				// originating machine never attributed them first, the restorer
				// claims them. Nothing on disk distinguishes "I ran this" from
				// "I restored this", so that is the honest limit of the
				// inference — the Desktop sidecar is what settles it properly.
				acct, src = liveAccount, ledger.AccountFromSync
			}
			info, serr := os.Stat(p)
			if serr != nil {
				return nil
			}
			// The transcript's own last record, not its mtime: a restore or a
			// checkout of the staged tree rewrites mtime, which would re-date
			// every session to the copy and, since End is half the change
			// fingerprint, force a rewrite of every row after it.
			end := info.ModTime().UTC()
			if a, ok := session.LastActivity(p); ok {
				end = a.At
			}
			// An unchanged transcript is normally skipped without reading it. It
			// still needs a pass when the attribution on offer OUTRANKS the stored
			// one, or a session first labelled by inference could never be upgraded
			// by its Desktop sidecar: the transcript it names never changes again,
			// so there would be no later occasion to look.
			_, localSrc := l.Attribution(id)
			prevSrc := localSrc
			if u := union[id].AccountSource; ledger.AccountRank(u) > ledger.AccountRank(prevSrc) {
				prevSrc = u
			}
			if l.Fresh(id, end, info.Size()) && ledger.AccountRank(src) <= ledger.AccountRank(prevSrc) {
				return nil
			}
			e := ledger.Entry{
				ID:            id,
				Slug:          slugOf(rel),
				End:           end,
				Bytes:         info.Size(),
				Title:         session.FirstPrompt(p),
				Seen:          time.Now().UTC(),
				Account:       acct,
				AccountSource: src,
			}
			if cwd, ok, cerr := project.CwdFromTranscript(p); cerr == nil && ok {
				e.Cwd = cwd
			}
			if l.Note(e) {
				added++
			}
			return nil
		})
		if walkErr != nil {
			return added, l.Count(), walkErr
		}
	}
	return added, l.Count(), l.Save()
}

// sessionIDsFrom picks the session ids out of a CLI root's allowlisted paths:
// projects/<slug>/<id>.jsonl only, matching the shape recordLedger records.
// Deeper files (subagents/, tool-results/) resolve to the same session id and
// are not separate sessions.
func sessionIDsFrom(files []string) map[string]bool {
	out := make(map[string]bool, len(files))
	for _, rel := range files {
		if !strings.HasPrefix(rel, "projects/") || !strings.HasSuffix(rel, ".jsonl") {
			continue
		}
		if strings.Count(rel, "/") != 2 {
			continue
		}
		if id := session.IDFromTranscriptRel(rel); id != "" {
			out[id] = true
		}
	}
	return out
}

// slugOf pulls the projects/<slug>/ segment out of a CLI-root-relative path.
func slugOf(rel string) string {
	rest := strings.TrimPrefix(rel, "projects/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return ""
}
