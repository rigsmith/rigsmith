package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// stackRedirects works out which package references cross from one member of a
// stackspace to another. Those are exactly the ones that would otherwise be
// fetched from a registry, so those are the ones an overlay has to redirect.
//
// Nothing here parses a project file. Every member is in one repository, so a
// reference from one to another is an intra-repo dependency — the same graph the
// release cascade already computes — and the ecosystem adapters already report
// it, including the package identity, which for .NET is the PackageId and not
// the file name. Those differ often enough that guessing from the filename is
// the classic way to redirect the wrong package.
//
// The result is keyed by ecosystem id: each writes its own kind of overlay.
// failed names the ecosystems whose scan errored. For those, "no links" is
// not an answer: nothing that acts on an empty answer — calling an overlay
// left over, say — may do so, and the failure itself is what to report.
func stackRedirects(ctx context.Context, root string, members []string) (map[string][]stackLink, []stackOrphan, map[string]error) {
	member := func(rel string) string {
		rel = filepath.ToSlash(rel)
		for _, m := range members {
			if rel == m || strings.HasPrefix(rel, m+"/") {
				return m
			}
		}
		return ""
	}

	out := map[string][]stackLink{}
	// Which members produce something, and which are depended on by another.
	// A fused repo nothing here consumes is the quiet mistake this catches.
	produces := map[string][]string{}
	consumed := map[string]bool{}
	failed := map[string]error{}
	for _, eco := range ecosystem.Default().All() {
		// Overlay ecosystems re-emit the base language's project rather than
		// owning it, so asking them would double-count what the base reports.
		if len(eco.Info().Overlays) > 0 {
			continue
		}
		ok, err := eco.Detect(ctx, root)
		if err != nil {
			failed[eco.Info().ID] = err
			continue
		}
		if !ok {
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: ".", IncludeUnversioned: true, IncludeRegistrySiblings: true})
		if err != nil {
			failed[eco.Info().ID] = err
			continue
		}
		// Where each package is produced, so a dependency on it can be pointed
		// at the project rather than at the registry.
		producer := map[string]plugin.Package{}
		for _, p := range resp.Packages {
			producer[p.Name] = p
			if m := member(p.Dir); m != "" {
				produces[m] = append(produces[m], p.Name)
			}
		}
		at := map[string]int{}
		var links []stackLink
		for _, p := range resp.Packages {
			from := member(p.Dir)
			for _, d := range p.Dependencies {
				// Only a registry-referenced sibling needs redirecting: a project
				// reference already resolves to the sources beside it.
				if !d.ViaRegistry {
					continue
				}
				prod, ok := producer[d.Name]
				if !ok {
					continue
				}
				// Within one member the projects already reference each other
				// directly; only a reference that leaves its member goes through
				// a registry and needs redirecting.
				if to := member(prod.Dir); to == "" || to == from {
					continue
				}

				to := member(prod.Dir)
				consumed[to] = true
				// One redirect per package, but every member that consumes it is
				// worth naming: "which repo here depends on which" is the question
				// people actually have, and a count cannot answer it.
				if i, dup := at[d.Name]; dup {
					if !slices.Contains(links[i].From, from) {
						links[i].From = append(links[i].From, from)
					}
					continue
				}
				at[d.Name] = len(links)
				links = append(links, stackLink{
					Redirect: plugin.Redirect{Package: d.Name, Path: filepath.ToSlash(prod.ManifestPath)},
					From:     []string{from},
					To:       to,
				})
			}
		}
		if len(links) > 0 {
			sort.Slice(links, func(i, j int) bool { return links[i].Package < links[j].Package })
			for i := range links {
				sort.Strings(links[i].From)
			}
			out[eco.Info().ID] = links
		}
	}

	var orphans []stackOrphan
	for _, m := range members {
		if consumed[m] || len(produces[m]) == 0 {
			continue
		}
		sort.Strings(produces[m])
		orphans = append(orphans, stackOrphan{Member: m, Produces: produces[m]})
	}
	return out, orphans, failed
}

// stackOrphan is a fused repo whose packages nothing else here references.
//
// Nearly always one of two mistakes, and silent either way: the wrong repo was
// fused, or the right one was and the consumer has since moved to a renamed
// fork of it. The stackspace imports, wires and builds — and changes nothing,
// because by identity there was never a link to redirect.
//
// An app is a leaf and belongs at the end of the graph, so a member marked
// owned is not reported.
type stackOrphan struct {
	Member   string
	Produces []string
}

// describe names one package as evidence. The full list is usually long and
// mostly demos and test projects; one recognisable name is what tells someone
// whether they fused what they meant to.
func (o stackOrphan) describe() string {
	sample := o.Produces[0]
	for _, p := range o.Produces {
		// Prefer something that looks like the library itself over its sidecars.
		if !strings.Contains(p, "Test") && !strings.Contains(p, "Demo") && !strings.Contains(p, "Example") {
			sample = p
			break
		}
	}
	more := ""
	if n := len(o.Produces) - 1; n > 0 {
		more = fmt.Sprintf(" (and %d more)", n)
	}
	return fmt.Sprintf("%s produces %s%s, which nothing here consumes", o.Member, sample, more)
}

// stackLink is one package that crosses from one member of the stackspace to
// another — the reference that would otherwise be fetched from a registry.
type stackLink struct {
	plugin.Redirect
	From []string // members that consume it
	To   string   // the member that produces it
}

// describe names the link the way someone looking at their own stackspace
// thinks of it: which of my repos depends on which.
func (l stackLink) describe() string {
	return fmt.Sprintf("%s  %s → %s", l.Package, strings.Join(l.From, ", "), l.To)
}

// redirectsOf drops the reporting detail the adapters have no use for.
func redirectsOf(links []stackLink) []plugin.Redirect {
	out := make([]plugin.Redirect, 0, len(links))
	for _, l := range links {
		out = append(out, l.Redirect)
	}
	return out
}

// stackOverlayReport is one ecosystem's answer about the build wiring.
type stackOverlayReport struct {
	Eco   string
	Links []stackLink
	Resp  plugin.LocalOverlayResponse
}

// stackCheckOverlay asks each ecosystem whether the redirects it needs are in
// effect, without changing anything.
//
// failed names the ecosystems that could not be scanned, keyed by id; no
// report is made for those, since an overlay cannot be judged against links
// that were never found.
func stackCheckOverlay(ctx context.Context, root string, members, writable []string) ([]stackOverlayReport, []stackOrphan, map[string]error) {
	var out []stackOverlayReport
	byEco, orphans, failed := stackRedirects(ctx, root, members)
	for _, eco := range ecosystem.Default().All() {
		// A scan that errored found no links, which is not the same as there
		// being none: the overlay it would have needed is still needed, and
		// calling it left over would be exactly wrong.
		if failed[eco.Info().ID] != nil {
			continue
		}
		// Asked even with nothing to redirect: an overlay left over from
		// members that have gone is a problem only the adapter can see,
		// and it is exactly when nothing crosses that it goes unnoticed.
		links := byEco[eco.Info().ID]
		resp, err := eco.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirectsOf(links), Writable: writable,
		})
		if err != nil || resp.Skipped {
			continue
		}
		out = append(out, stackOverlayReport{Eco: eco.Info().ID, Links: links, Resp: resp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Eco < out[j].Eco })
	return out, orphans, failed
}

// stackReportScanFailures says which ecosystems could not be scanned, in a
// stable order. Silence here would read as "nothing crosses", which is the
// one thing a failed scan cannot say.
func stackReportScanFailures(out io.Writer, failed map[string]error) {
	ids := make([]string, 0, len(failed))
	for id := range failed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(out, "✗ %s: could not scan for package references — %v\n", id, failed[id])
	}
}
