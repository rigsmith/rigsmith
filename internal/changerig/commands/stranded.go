package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Stranded is a changeset that exists but can never release: the planner has
// no package to attribute it to, so it sits in .changeset/ contributing
// nothing to any version bump or changelog, indefinitely.
type Stranded struct {
	ID     string // file name without extension
	Reason string // why it produced no plan entry
}

// FindStranded explains why a set of changesets yielded an empty release plan,
// one file at a time.
//
// This is the difference between "nothing to release" and "sixteen changesets
// are silently doing nothing". A hand-written changeset that omits the package
// line parses cleanly, is counted as found, and is then attributed to no
// package — so the tool reported changesets *and* an empty plan and left the
// contradiction for a human to notice. In this repo it went unnoticed across a
// release: 1.5.0 shipped three weeks of work whose changesets had been inert
// since before 1.4.0.
//
// Only genuinely stranded files are returned. A changeset held back by
// prerelease mode, or one naming a package with no bump this run, is doing what
// it should and is not reported.
func FindStranded(changesets []*changeset.Changeset, pkgs []plugin.Package, cfg *config.Config) []Stranded {
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[p.Name] = true
	}

	var out []Stranded
	for _, cs := range changesets {
		if cs == nil {
			continue
		}
		if len(cs.Releases) == 0 {
			out = append(out, Stranded{ID: cs.ID, Reason: "names no package"})
			continue
		}

		var unknown, ignored []string
		live := 0
		for _, r := range cs.Releases {
			switch {
			case !known[r.Name]:
				unknown = append(unknown, r.Name)
			case cfg != nil && cfg.IsIgnored(r.Name):
				ignored = append(ignored, r.Name)
			default:
				live++
			}
		}
		if live > 0 {
			continue // it names something releasable; an empty plan is not this file's doing
		}
		switch {
		case len(unknown) > 0:
			out = append(out, Stranded{ID: cs.ID, Reason: "names unknown package(s): " + strings.Join(unknown, ", ")})
		case len(ignored) > 0:
			out = append(out, Stranded{ID: cs.ID, Reason: "names only ignored package(s): " + strings.Join(ignored, ", ")})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StrandedHint suggests the fix, naming the package when the workspace has
// exactly one releasable one (the common single-module case, where the right
// answer is unambiguous and worth spelling out).
func StrandedHint(tool string, pkgs []plugin.Package, cfg *config.Config) string {
	var releasable []string
	for _, p := range pkgs {
		if cfg == nil || !cfg.IsIgnored(p.Name) {
			releasable = append(releasable, p.Name)
		}
	}
	if len(releasable) == 1 {
		return fmt.Sprintf("Name the package in the front matter — `%s add -t <type> -p %s` writes it correctly.", tool, releasable[0])
	}
	return fmt.Sprintf("Name the package in the front matter — `%s add` writes it correctly.", tool)
}
