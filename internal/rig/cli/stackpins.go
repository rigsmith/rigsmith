package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
)

// stackStackOnlyPins names the packages this member references that another
// member of the stackspace produces.
//
// These are the pins the build overlay satisfies from source, and they are
// exactly what a plain checkout of a proposed branch cannot resolve: the branch
// references a package by identity, the stackspace answers it with a sibling
// directory, and a bare clone has neither that directory nor, necessarily, a
// feed carrying the version the branch asks for. Nothing says so until restore
// fails, and NuGet in particular can fail without printing why.
//
// Republished ids count and are the case most worth naming: with publishesAs or
// publishPrefix the consumer references an id that only the producer's own feed
// ever carries, so "it is on nuget.org" is not even the right question.
//
// The scan is the same one `wire` and `doctor` run. Its failures are returned
// rather than swallowed, because "no links found" and "the scan could not look"
// are different answers and only the first one may be reported as reassurance.
func stackStackOnlyPins(ctx context.Context, root string, m *stackManifest, member string) ([]stackLink, map[string]error) {
	byEco, _, _, failed := stackRedirects(ctx, root, m.names(), m.publishing())
	var out []stackLink
	for _, links := range byEco {
		for _, l := range links {
			// Only what this member consumes. A link into some other member is
			// real, and says nothing about the branch being proposed here.
			for _, from := range l.From {
				if from == member {
					out = append(out, l)
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, failed
}

// stackReportStackOnlyPins says what the proposed branch needs and does not
// carry. Silent when there is nothing to say, and silent when the scan failed —
// a warning built on a scan that could not look would be worse than none.
//
// Written as a note under the proposal rather than a refusal: a branch whose
// pins resolve only here is the normal, intended shape of a stackspace
// proposal, not a mistake. What is missing is only that nothing said so.
func stackReportStackOnlyPins(out io.Writer, links []stackLink, member string, failed map[string]error) {
	if len(failed) > 0 || len(links) == 0 {
		return
	}
	fmt.Fprintf(out, "note: %s\n", stackPinsHeadline(links, member))
	for _, l := range links {
		fmt.Fprintf(out, "        %s\n", l.describe())
	}
	fmt.Fprintf(out, "      a plain checkout of this branch has no source for %s — pack it from the stackspace,\n"+
		"      or make sure the feed the branch expects carries the version it pins\n",
		pluralIt(len(links)))
}

func stackPinsHeadline(links []stackLink, member string) string {
	if len(links) == 1 {
		return fmt.Sprintf("%s references 1 package that this stackspace provides from source, not a feed:", member)
	}
	return fmt.Sprintf("%s references %d packages that this stackspace provides from source, not a feed:", member, len(links))
}

func pluralIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// stackPinNames is the packages alone, for a caller that wants the fact without
// the paragraph — `status`, or a test asserting what was found.
func stackPinNames(links []stackLink) []string {
	names := make([]string, 0, len(links))
	for _, l := range links {
		names = append(names, l.Package)
	}
	return names
}
