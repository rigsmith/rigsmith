package sessions

import (
	"path/filepath"
	"sort"

	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// Split is one session whose transcript exists under more than one project
// directory.
//
// It happens when a conversation continues in a different directory: Claude Code
// files by working directory, so it opens a second transcript under the new slug
// while the first stays frozen at the moment the work moved. Both are real files
// with the same id, and anything that resolves by directory can land on the
// frozen one — which looks exactly like a session losing everything after that
// date.
type Split struct {
	ID string `json:"id"`
	// Keep is the copy with the newest activity: the one the tools use.
	Keep string `json:"keep"`
	// Others are the copies not chosen, newest first.
	Others []string `json:"others"`
}

// StaleSidecar is a Desktop sidecar naming a working directory that no longer
// holds the session's transcript.
//
// This is what a split session actually presents as. Desktop resolves a session
// through the directory its sidecar records, so once work moves elsewhere the
// sidecar keeps naming the old one and Desktop opens the transcript frozen
// there — silently, showing the conversation as it stood the day it split.
type StaleSidecar struct {
	ID string `json:"id"`
	// Says is the directory the sidecar records.
	Says string `json:"says"`
	// Actually is the directory the newest transcript is filed under.
	Actually string `json:"actually"`
	Title    string `json:"title,omitempty"`
}

// Health is what is wrong with how sessions are filed on this machine.
//
// Detection lives here rather than in any one front end because all three need
// it: `clauderig doctor` reports it, the dashboard shows it, and the window
// offers to act on it. One implementation means they cannot disagree about
// whether something is wrong.
type Health struct {
	Splits []Split        `json:"splits,omitempty"`
	Stale  []StaleSidecar `json:"stale,omitempty"`
}

// OK reports whether nothing needs attention.
func (h Health) OK() bool { return len(h.Splits) == 0 && len(h.Stale) == 0 }

// CheckHealth looks for split sessions and stale Desktop sidecars.
//
// Read-only, and it never proposes a repair by itself. Which copy of a split
// session is wanted is usually obvious and occasionally not: when two copies
// have genuinely diverged, each holding turns the other lacks, discarding
// either loses a conversation. That is a decision, not a repair.
func CheckHealth(targets []search.Target, roots []session.Root) Health {
	var h Health

	paths, extra := TranscriptPathsAll(targets, CLISource)
	ids := make([]string, 0, len(extra))
	for id := range extra {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		h.Splits = append(h.Splits, Split{ID: id, Keep: paths[id], Others: extra[id]})
	}

	if len(roots) > 0 {
		idx := session.Build(roots)
		stale := make([]StaleSidecar, 0, 4)
		for id, meta := range idx {
			live := paths[id]
			// No transcript here, or a sidecar with no directory recorded, and
			// there is nothing to disagree about.
			if live == "" || meta.Cwd == "" {
				continue
			}
			// A synced sidecar can carry a $HOME/… template rather than a real
			// path; those cannot be compared against a local slug and are not
			// evidence of anything.
			if !filepath.IsAbs(meta.Cwd) {
				continue
			}
			if filepath.Base(filepath.Dir(live)) == project.Flatten(meta.Cwd) {
				continue
			}
			stale = append(stale, StaleSidecar{
				ID: id, Says: meta.Cwd, Title: meta.Title,
				Actually: filepath.Base(filepath.Dir(live)),
			})
		}
		sort.Slice(stale, func(i, j int) bool { return stale[i].ID < stale[j].ID })
		h.Stale = stale
	}
	return h
}
