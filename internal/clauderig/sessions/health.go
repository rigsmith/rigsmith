package sessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// Copy is one transcript of a split session, with enough to judge it by.
type Copy struct {
	Path  string    `json:"path"`
	Slug  string    `json:"slug"`
	Lines int       `json:"lines"`
	Bytes int64     `json:"bytes"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	// Only is how many records this copy holds that the kept one does not.
	// Zero means nothing would be lost by setting it aside.
	Only int  `json:"only"`
	Keep bool `json:"keep"`
}

// SplitDetail is a split session with both copies described, and whether the
// older ones can be set aside without losing anything.
type SplitDetail struct {
	ID     string `json:"id"`
	Copies []Copy `json:"copies"`
	// Safe is true when every copy that is not kept is wholly contained in the
	// one that is. When false, the copies have genuinely diverged and choosing
	// between them loses turns — which is a decision for a person, not a repair
	// for a tool.
	Safe bool `json:"safe"`
	// Diverged counts the records that exist only in copies not being kept.
	Diverged int `json:"diverged"`
}

// Describe reads both sides of a split and reports what each holds.
//
// Records are compared by uuid rather than by offset or length: an append-only
// transcript usually makes the older copy a prefix of the newer, but a session
// resumed twice from the same point does not, and "shorter" is not the same as
// "contained".
func Describe(s Split) SplitDetail {
	out := SplitDetail{ID: s.ID, Safe: true}
	keep, keepIDs := describeCopy(s.Keep)
	keep.Keep = true
	out.Copies = append(out.Copies, keep)

	for _, p := range s.Others {
		c, ids := describeCopy(p)
		for id := range ids {
			if !keepIDs[id] {
				c.Only++
			}
		}
		if c.Only > 0 {
			out.Safe = false
			out.Diverged += c.Only
		}
		out.Copies = append(out.Copies, c)
	}
	return out
}

// describeCopy reads one transcript's shape and the record ids it holds.
func describeCopy(path string) (Copy, map[string]bool) {
	c := Copy{Path: path, Slug: filepath.Base(filepath.Dir(path))}
	ids := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return c, ids
	}
	defer f.Close()
	if info, serr := f.Stat(); serr == nil {
		c.Bytes = info.Size()
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 {
			var probe struct {
				UUID string    `json:"uuid"`
				At   time.Time `json:"timestamp"`
			}
			if json.Unmarshal(line, &probe) == nil {
				c.Lines++
				if probe.UUID != "" {
					ids[probe.UUID] = true
				}
				if !probe.At.IsZero() {
					if c.First.IsZero() {
						c.First = probe.At
					}
					c.Last = probe.At
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return c, ids
}

// Consolidate moves every copy of a split session except the kept one into
// parkDir, and reports where they went.
//
// Moved, never deleted. The whole fault this repairs is a conversation that
// appeared to vanish; answering it by actually deleting one would be a poor
// joke. Refuses outright when the copies have diverged — Describe decides that,
// and a caller must not override it, because "this copy has three turns the
// other lacks" has no safe automatic answer.
func Consolidate(s Split, parkDir string) (parked []string, err error) {
	d := Describe(s)
	if !d.Safe {
		return nil, fmt.Errorf("these copies have diverged: %d record(s) exist only in the older one — "+
			"open both before choosing", d.Diverged)
	}
	if err := os.MkdirAll(parkDir, 0o755); err != nil {
		return nil, err
	}
	for _, p := range s.Others {
		// Named for where it came from, so a parked file can be put back.
		dest, rerr := reserveParkName(parkDir, filepath.Base(filepath.Dir(p))+"__"+filepath.Base(p))
		if rerr != nil {
			return parked, fmt.Errorf("could not park %s: %w", p, rerr)
		}
		if err := os.Rename(p, dest); err != nil {
			_ = os.Remove(dest)
			return parked, fmt.Errorf("could not park %s: %w", p, err)
		}
		parked = append(parked, dest)
	}
	return parked, nil
}

// reserveParkName claims a free filename in parkDir and returns it, having
// created an empty file to hold the name.
//
// Rename overwrites its destination, and the destination here is a transcript
// parked by an earlier run — the only surviving copy of a conversation, since
// this function moves rather than deletes. A session that splits into the same
// project directory twice generates the same name twice, and the second park
// would silently destroy the first. Suffixed instead: -2, -3, and so on.
func reserveParkName(parkDir, name string) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n < 1000; n++ {
		candidate := filepath.Join(parkDir, stem+ext)
		if n > 1 {
			candidate = filepath.Join(parkDir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		}
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", name, parkDir)
}
