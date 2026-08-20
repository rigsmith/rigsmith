package commands

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
)

// Unmentioned is a changeset whose body appears to be about some of the
// packages it names, but not all of them.
//
// A changeset renders ONE body into EVERY package it names. That is the
// upstream changesets design and changerig matches it deliberately — the
// documented answer to "these packages need different entries" is to write more
// than one changeset. The failure mode is that nothing says so: naming a second
// package costs one line in the frontmatter, and the cost lands in a changelog
// nobody re-reads, for the package the author was least focused on.
//
// So this is a nudge toward splitting, not a rule. It never blocks a release.
type Unmentioned struct {
	ID        string   // file name without extension
	Mentioned []string // packages the body actually names
	Missing   []string // packages that will get this same text verbatim
}

// Hint is the suggested fix, naming the first missing package.
func (u Unmentioned) Hint(tool string) string {
	return fmt.Sprintf("Consider splitting: %s add -p %s", tool, u.Missing[0])
}

// FindUnmentioned reports changesets that name several packages while their body
// only talks about some of them.
//
// Mentioning a package by name is the whole signal. It is crude on purpose: a
// body that genuinely covers several packages nearly always names them, and
// anything semantic would be both slower and less predictable than the author's
// own words. Erring toward silence is deliberate — a missed nudge costs nothing,
// while a wrong one trains people to ignore the next.
//
// Packages sharing a `fixed` or `linked` group are skipped. Those move together
// by configuration, so one body describing the family is the intended shape
// rather than an oversight.
func FindUnmentioned(changesets []*changeset.Changeset, cfg *config.Config) []Unmentioned {
	var out []Unmentioned
	for _, cs := range changesets {
		if cs == nil || len(cs.Releases) < 2 {
			continue // one package: the body cannot be about the wrong one
		}
		if strings.TrimSpace(cs.Summary) == "" {
			continue // nothing to judge; an empty body is a different problem
		}
		names := make([]string, 0, len(cs.Releases))
		for _, r := range cs.Releases {
			names = append(names, r.Name)
		}
		if sameLockstepGroup(names, cfg) {
			continue
		}

		var mentioned, missing []string
		for _, name := range names {
			if mentionsPackage(cs.Summary, name) {
				mentioned = append(mentioned, name)
			} else {
				missing = append(missing, name)
			}
		}
		// Report only a partial match. All mentioned is a good changeset; none
		// mentioned is a body written without naming anything ("fix the parser"),
		// which is ordinary and says nothing about which package it means.
		if len(mentioned) == 0 || len(missing) == 0 {
			continue
		}
		out = append(out, Unmentioned{ID: cs.ID, Mentioned: mentioned, Missing: missing})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// sameLockstepGroup reports whether every name sits in one `fixed` or `linked`
// group. Not merely "some pair overlaps": a changeset spanning a linked family
// AND an unrelated package is exactly the case worth flagging.
func sameLockstepGroup(names []string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, group := range append(append([][]string{}, cfg.Fixed...), cfg.Linked...) {
		inGroup := make(map[string]bool, len(group))
		for _, g := range group {
			inGroup[g] = true
		}
		all := true
		for _, n := range names {
			if !inGroup[n] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// mentionsPackage reports whether body names pkg — either in full, or by the
// distinctive last segment of a qualified name (`Avalloy.Themes` → "Themes",
// `github.com/acme/widget` → "widget", `@scope/widget` → "widget"), which is how
// people actually write about a package in prose.
//
// Matched on word boundaries so a package named `ui` is not "mentioned" by the
// word "building", and only for segments long enough to be meaningful — a short
// tail like `go` or `v2` would match constantly and report nothing.
func mentionsPackage(body, pkg string) bool {
	if wordIn(body, pkg) {
		return true
	}
	for _, short := range shortNames(pkg) {
		if wordIn(body, short) {
			return true
		}
	}
	return false
}

const shortestMeaningfulSegment = 4

func shortNames(pkg string) []string {
	var out []string
	tail := pkg
	if i := strings.LastIndexAny(tail, "/"); i >= 0 {
		tail = tail[i+1:]
	}
	if tail != pkg && len(tail) >= shortestMeaningfulSegment {
		out = append(out, tail)
	}
	if i := strings.LastIndex(tail, "."); i >= 0 {
		if dotted := tail[i+1:]; len(dotted) >= shortestMeaningfulSegment {
			out = append(out, dotted)
		}
	}
	return out
}

func wordIn(body, word string) bool {
	if word == "" {
		return false
	}
	re, err := regexp.Compile(`(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(word) + `($|[^\p{L}\p{N}])`)
	if err != nil {
		return false
	}
	return re.MatchString(body)
}

// PrintUnmentioned writes the nudge for each changeset that looks like it wants
// splitting. Shown by both `status` and `version`: status is the cheap moment,
// before the changelogs are written, but version is where someone who never runs
// status still has a chance to stop.
func PrintUnmentioned(w io.Writer, found []Unmentioned, tool string) {
	for _, u := range found {
		fmt.Fprintf(w, "%s\n", WarnStyle.Render(fmt.Sprintf(
			"⚠ %s names %d packages but only mentions %s.",
			u.ID, len(u.Mentioned)+len(u.Missing), quoteList(u.Mentioned))))
		fmt.Fprintf(w, "  %s\n", DimStyle.Render(fmt.Sprintf(
			"%s will get this same text verbatim.", quoteList(u.Missing))))
		fmt.Fprintf(w, "  %s\n", DimStyle.Render(u.Hint(tool)))
	}
}

func quoteList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, `"`+n+`"`)
	}
	switch len(quoted) {
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}
