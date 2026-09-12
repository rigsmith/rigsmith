// Package contents answers "what is actually in my sync repo", by category and
// by size.
//
// A byte total on its own invites the wrong lever. On a real clauderig repo,
// 1,618 MB of a 1,620 MB checkout was conversation — so squashing history, which
// is what a large number makes people reach for, would have moved nothing. The
// question worth answering is which KIND of thing is large, because that is what
// decides whether the answer is retention, chunking, or leaving it alone.
//
// Metadata only: one stat per file, nothing is read.
package contents

import (
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/ledger"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// Group is one category's share.
type Group struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
}

// Report is the whole scan.
type Report struct {
	Groups []Group `json:"groups"`
	Files  int     `json:"files"`
	Bytes  int64   `json:"bytes"`
}

// Scan walks a repo and totals it by category. A file that vanishes mid-walk is
// skipped rather than fatal: a sync may be running.
func Scan(dir string) (Report, error) {
	var rep Report
	byName := map[string]*Group{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// .git is the repo's own storage, not its contents. Counting it
			// here would double every byte and answer a different question —
			// `repo` reports it separately, which is where it belongs.
			if rel == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		// A part is the inside of the rollout whose index sits beside it, and
		// that index is counted at the conversation's size — so the part
		// itself counts for nothing, or the session would be counted twice
		// and the index would be counted as a few hundred bytes.
		if rolloutstore.IsPartPath(rel) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if _, rest, ok := strings.Cut(rel, "/"); ok && rollout.IsRolloutRel(rest) {
			if logical, serr := rolloutstore.Stat(p); serr == nil {
				info = logical
			}
		}
		name, detail := classify(rel)
		g := byName[name]
		if g == nil {
			g = &Group{Name: name, Detail: detail}
			byName[name] = g
		}
		g.Files++
		g.Bytes += info.Size()
		rep.Files++
		rep.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return rep, err
	}

	for _, g := range byName {
		rep.Groups = append(rep.Groups, *g)
	}
	sort.Slice(rep.Groups, func(i, j int) bool {
		if rep.Groups[i].Bytes != rep.Groups[j].Bytes {
			return rep.Groups[i].Bytes > rep.Groups[j].Bytes
		}
		return rep.Groups[i].Name < rep.Groups[j].Name
	})
	return rep, nil
}

// classify names what a staged path is, in the reader's vocabulary rather than
// the layout's.
func classify(rel string) (name, detail string) {
	first, rest, nested := strings.Cut(rel, "/")
	switch {
	case first == ledger.DirName:
		return "codexrig records", "the permanent session index"
	case first == "journal":
		return "codexrig records", "the activity journal"
	case !nested:
		return "codexrig records", "the manifest and device registry"
	}

	switch {
	case rollout.IsRolloutRel(rest), rolloutstore.IsPartPath(rest):
		return "sessions", "the conversations themselves"
	case strings.HasPrefix(rest, "skills/"):
		return "skills", "what you have taught Codex"
	case strings.HasPrefix(rest, "prompts/"):
		return "prompts", "your saved prompts"
	case strings.HasPrefix(rest, "rules/"):
		return "rules", "execution policy"
	case rest == "session_index.jsonl":
		return "sessions", "the thread-name index"
	case path.Base(rest) == "AGENTS.md" || path.Base(rest) == "AGENTS.override.md":
		return "instructions", "AGENTS.md"
	case strings.HasSuffix(rest, ".toml"):
		return "config", "config.toml and profile overlays"
	default:
		return "config", "settings and everything else"
	}
}

// MinShare is the share below which a category is folded into "other".
const MinShare = 0.02

// Fold collapses the small categories, but only when at least two qualify —
// folding one into "other" renames it without telling the reader anything.
func (r Report) Fold() Report {
	if r.Bytes == 0 {
		return r
	}
	var big []Group
	var small []Group
	for _, g := range r.Groups {
		if float64(g.Bytes)/float64(r.Bytes) < MinShare {
			small = append(small, g)
			continue
		}
		big = append(big, g)
	}
	if len(small) < 2 {
		return r
	}
	other := Group{Name: "other"}
	var names []string
	for _, g := range small {
		other.Files += g.Files
		other.Bytes += g.Bytes
		names = append(names, g.Name)
	}
	if len(names) > 3 {
		other.Detail = strings.Join(names[:3], ", ") + " and " + itoa(len(names)-3) + " more"
	} else {
		other.Detail = strings.Join(names, ", ")
	}
	out := r
	out.Groups = append(big, other)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
