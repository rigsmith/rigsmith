// Package commitsource synthesizes in-memory changesets from conventional
// commits, so commit-based versioning is a second *source adapter* rather than a
// parallel engine: the changesets it produces feed the exact same
// planner.Plan() as on-disk changeset files. Attribution — deciding which
// package(s) a commit bumps — is the only genuinely new logic; everything
// downstream (cascade, grouping, prerelease, snapshot, changelog) is shared.
//
// See docs/COMMIT-VERSIONING.md for the design.
package commitsource

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/core/changeset"
	"github.com/rigsmith/core/config"
	"github.com/rigsmith/core/gitutil"
	"github.com/rigsmith/core/plugin"
)

// headerRe matches a conventional-commit subject: type(scope)!: description.
// Groups: 1=type, 2=scope, 3=`!`, 4=description.
var headerRe = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^)]*)\))?(!)?:\s*(.*)$`)

// breakingFooterRe matches a `BREAKING CHANGE:`/`BREAKING-CHANGE:` footer token
// at the start of a body line, per the Conventional Commits spec (uppercase).
var breakingFooterRe = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)

// header is the parsed conventional-commit header of a commit subject.
type header struct {
	typ      string
	scope    string
	breaking bool
	desc     string // subject with the type/scope prefix stripped
}

// parseHeader parses a conventional-commit subject. ok=false for a
// non-conventional subject (a merge commit, a freeform message) — which commit
// mode treats as "no release".
func parseHeader(subject string) (header, bool) {
	m := headerRe.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil {
		return header{}, false
	}
	return header{
		typ:      strings.ToLower(m[1]),
		scope:    m[2],
		breaking: m[3] == "!",
		desc:     strings.TrimSpace(m[4]),
	}, true
}

// Synthesize converts commits into in-memory changesets, attributing each
// commit to the package(s) it bumps. A non-conventional commit produces nothing
// (no release). A commit attributed to no package produces nothing. Each
// (commit, package) pair becomes one synthetic changeset carrying the commit's
// conventional type and breaking flag, so the planner derives the bump exactly
// as it would for a type-driven changeset file.
//
// Attribution is path-based by default — a commit bumps every package owning at
// least one of its changed files (most-specific dir wins). When the config maps
// the commit's scope to a known package, that scope wins and the commit
// attributes to that single package instead.
func Synthesize(commits []gitutil.Commit, packages []plugin.Package, repoRoot string, cfg *config.Config) []*changeset.Changeset {
	known := make(map[string]bool, len(packages))
	for _, p := range packages {
		known[p.Name] = true
	}
	scopes := cfg.Versioning.Scopes

	var out []*changeset.Changeset
	for _, c := range commits {
		h, ok := parseHeader(c.Subject)
		if !ok {
			continue
		}
		breaking := h.breaking || breakingFooterRe.MatchString(c.Body)

		// changelogen-style: when the body carries a `BREAKING CHANGE:` footer,
		// surface its description as a continuation line under the bullet (the
		// changelog writer indents continuation lines two spaces). The subject
		// stays the headline.
		summary := h.desc
		if note := breakingNote(c.Body); note != "" {
			summary = h.desc + "\n" + note
		}

		var names []string
		if scopes != nil && h.scope != "" {
			if pkg, mapped := scopes[h.scope]; mapped && known[pkg] {
				names = []string{pkg}
			}
		}
		if names == nil {
			names = attributeByPath(c.Files, packages, repoRoot)
		}
		if len(names) == 0 {
			continue
		}

		releases := make([]changeset.Release, len(names))
		for i, n := range names {
			// Bump left as BumpNone so the planner derives it from the type via
			// the configured changelogGroups — exactly like a type-driven changeset.
			releases[i] = changeset.Release{Name: n, Bump: changeset.BumpNone}
		}
		out = append(out, &changeset.Changeset{
			Releases: releases,
			Summary:  summary,
			Type:     h.typ,
			Breaking: breaking,
			ID:       shortHash(c.Hash),
			Commit:   c.Hash,
		})
	}
	return out
}

// breakingNote extracts the description of a `BREAKING CHANGE:` /
// `BREAKING-CHANGE:` footer: the text after the token, plus any continuation
// lines up to the next blank line, collapsed to a single line. Returns "" when
// there is no footer (a `!`-only breaking change carries no separate note).
func breakingNote(body string) string {
	loc := breakingFooterRe.FindStringIndex(body)
	if loc == nil {
		return ""
	}
	var parts []string
	for i, line := range strings.Split(body[loc[1]:], "\n") {
		t := strings.TrimSpace(line)
		if i > 0 && t == "" {
			break // a blank line ends the footer block
		}
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// attributeByPath returns the names of the packages that own at least one of the
// changed files. A file maps to the most specific (deepest) package directory
// containing it, so a file inside a nested package attributes only to the inner
// one. Files under no package attribute to nothing. Names are returned in stable
// (sorted) order.
func attributeByPath(files []string, packages []plugin.Package, repoRoot string) []string {
	hit := map[string]bool{}
	for _, f := range files {
		best := ""
		bestLen := -1
		for _, p := range packages {
			dir := filepath.Clean(filepath.Join(repoRoot, p.Dir))
			if !isUnder(f, dir) {
				continue
			}
			if len(dir) > bestLen {
				best = p.Name
				bestLen = len(dir)
			}
		}
		if best != "" {
			hit[best] = true
		}
	}
	if len(hit) == 0 {
		return nil
	}
	names := make([]string, 0, len(hit))
	for n := range hit {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// isUnder reports whether file is dir itself or lives beneath it.
func isUnder(file, dir string) bool {
	dir = filepath.Clean(dir)
	return file == dir || strings.HasPrefix(file, dir+string(filepath.Separator))
}

// shortHash abbreviates a commit SHA to 7 chars for the synthetic changeset ID.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
