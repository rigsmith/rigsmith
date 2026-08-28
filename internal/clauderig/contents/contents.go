// Package contents answers what is actually inside the sync repo.
//
// "1.6 GB across 3,391 files" says the repo is large without saying what it is
// large WITH, and the two have different remedies: transcripts shrink by
// tightening retention, attachments by excluding them from the allowlist,
// backups by not keeping them at all. A total invites pruning history, which on
// a real repo would have been the wrong lever — 1,618 MB of a 1,620 MB checkout
// was conversation, and no amount of squashing touches that.
package contents

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Group is one category of file, with what it costs.
type Group struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
}

// Report is the breakdown, largest first, plus the totals it was derived from.
type Report struct {
	Groups []Group `json:"groups"`
	Files  int     `json:"files"`
	Bytes  int64   `json:"bytes"`
}

// classify names the category a repo-relative path belongs to. Ordered: the
// first rule that matches wins, so the specific cases sit above the general
// ones they would otherwise be swallowed by.
func classify(rel string) (name, detail string) {
	slash := filepath.ToSlash(rel)
	base := filepath.Base(slash)
	first, rest, _ := strings.Cut(slash, "/")

	switch {
	// clauderig's own records, which live at the top level beside the roots.
	case first == "journal" || first == "index":
		return "clauderig records", "the activity journal and session index"
	case !strings.Contains(slash, "/"):
		return "clauderig records", "the manifest and device registry"

	// Desktop: the session list is metadata about conversations kept elsewhere,
	// and is worth separating from the app's own configuration.
	case strings.HasPrefix(first, "desktop") && strings.Contains(rest, "claude-code-sessions"):
		return "Desktop session index", "sidecars naming conversations held in the CLI tree"
	case strings.HasPrefix(first, "desktop"):
		return "Desktop config", "profile settings and app state"
	}

	if inProjects := strings.Contains(slash, "/projects/"); inProjects {
		switch {
		case strings.HasSuffix(base, ".pre-import"):
			return "transcript backups", "copies Claude Code kept before moving a session"
		case strings.Contains(slash, "/memory/") || base == "MEMORY.md":
			return "memory", "durable notes, exempt from retention"
		case strings.HasSuffix(base, ".jsonl"):
			return "transcripts", "the conversations themselves"
		}
		return "attachments & tool output", "files a session produced or was given"
	}

	switch rest, _, _ = strings.Cut(rest, "/"); rest {
	case "plugins":
		return "plugins", "marketplace and plugin data"
	case "skills", "commands", "agents", "plans":
		return "skills, commands & agents", "what you have taught Claude Code"
	}
	return "config", "settings and everything else"
}

// Scan walks the checkout and buckets it. Metadata only — nothing is read — so
// this costs a stat per file rather than a byte of I/O per byte stored.
//
// .git is skipped: it is history, reported separately, and not part of what the
// repo is keeping.
func Scan(dir string) (Report, error) {
	byName := map[string]*Group{}
	var rep Report

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			// A file that vanished mid-walk is live churn, not a failure worth
			// abandoning the whole report over.
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
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
	// Largest first: the point of the breakdown is which one to act on, and that
	// is almost always the top line.
	sort.Slice(rep.Groups, func(i, j int) bool {
		if rep.Groups[i].Bytes != rep.Groups[j].Bytes {
			return rep.Groups[i].Bytes > rep.Groups[j].Bytes
		}
		return rep.Groups[i].Name < rep.Groups[j].Name
	})
	return rep, nil
}

// MinShare is the share of the checkout a category has to reach to earn a line
// of its own. Below it the row costs a line to say "rounds to zero".
const MinShare = 0.02

// Fold collapses categories under MinShare into a single "other" row, kept last
// whatever its size. The breakdown exists to show which one thing to act on, and
// a tail of rows all reading 0% buries it under its own precision.
//
// Only folds when at least two categories qualify: replacing one named row with
// an "other" that contains exactly it loses the name and gains nothing.
func (r Report) Fold() Report {
	if r.Bytes <= 0 {
		return r
	}
	var kept, small []Group
	for _, g := range r.Groups {
		if float64(g.Bytes)/float64(r.Bytes) < MinShare {
			small = append(small, g)
			continue
		}
		kept = append(kept, g)
	}
	if len(small) < 2 {
		return r
	}

	other := Group{Name: "other"}
	names := make([]string, 0, len(small))
	for _, g := range small {
		other.Files += g.Files
		other.Bytes += g.Bytes
		names = append(names, g.Name)
	}
	// Named, but not all of them: ten categories on one line is a row nobody
	// finishes reading, and the tail of the tail is not what anyone came for.
	// small is already largest-first, so these are the ones worth naming.
	const show = 3
	if len(names) > show {
		other.Detail = strings.Join(names[:show], ", ") +
			fmt.Sprintf(" and %d more", len(names)-show)
	} else {
		other.Detail = strings.Join(names, ", ")
	}

	out := r
	out.Groups = append(kept, other)
	return out
}
