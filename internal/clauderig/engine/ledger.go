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
func recordLedger(stagingDir, device string) (added int, total int, err error) {
	l, err := ledger.Open(stagingDir, device)
	if err != nil {
		return 0, 0, err
	}
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
			info, serr := os.Stat(p)
			if serr != nil {
				return nil
			}
			end := info.ModTime().UTC()
			if l.Fresh(id, end, info.Size()) {
				return nil
			}
			e := ledger.Entry{
				ID:    id,
				Slug:  slugOf(rel),
				End:   end,
				Bytes: info.Size(),
				Title: session.FirstPrompt(p),
				Seen:  time.Now().UTC(),
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

// slugOf pulls the projects/<slug>/ segment out of a CLI-root-relative path.
func slugOf(rel string) string {
	rest := strings.TrimPrefix(rel, "projects/")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i]
	}
	return ""
}
