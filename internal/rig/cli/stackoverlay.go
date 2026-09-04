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
// publishing says how a member's packages may be known beyond the ids they
// declare — a fork republished under its own id. A reference to such an id is
// redirected to the project producing the original, and the overlay is keyed
// on the id the consumer wrote, since that is what MSBuild matches.
//
// The result is keyed by ecosystem id: each writes its own kind of overlay.
// notes are things worth saying that are neither a link nor an orphan: a
// republishing rule naming a package nothing here produces.
func stackRedirects(ctx context.Context, root string, members []string, publishing map[string]stackPublishing) (map[string][]stackLink, []stackOrphan, []string) {
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
	var notes []string
	// Every republished id that turned out to name a real package, so a rule
	// that never matched anything can be reported afterwards.
	aliasUsed := map[string]map[string]bool{}
	for _, eco := range ecosystem.Default().All() {
		// Overlay ecosystems re-emit the base language's project rather than
		// owning it, so asking them would double-count what the base reports.
		if len(eco.Info().Overlays) > 0 {
			continue
		}
		if ok, err := eco.Detect(ctx, root); err != nil || !ok {
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: ".", IncludeUnversioned: true, IncludeRegistrySiblings: true})
		if err != nil {
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
		// The ids a member republishes under, resolved to the projects that
		// produce the originals. Only ids the member really produces count —
		// a rule for a package it does not build would redirect a consumer
		// to nothing, and the overlay would report that as a missing project.
		alias := map[string]string{} // republished id -> produced id
		for m, pub := range publishing {
			for _, orig := range produces[m] {
				if as, ok := pub.As[orig]; ok {
					alias[as] = orig
					if aliasUsed[m] == nil {
						aliasUsed[m] = map[string]bool{}
					}
					aliasUsed[m][orig] = true
					continue
				}
				if pub.Prefix != "" {
					alias[pub.Prefix+orig] = orig
				}
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
				via := ""
				if !ok {
					// Not produced under that id here — but perhaps under the
					// one it was republished from.
					orig, republished := alias[d.Name]
					if !republished {
						continue
					}
					prod, ok = producer[orig]
					if !ok {
						continue
					}
					via = orig
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
					Via:      via,
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
	// A republishing rule for a package the member does not produce is
	// almost always a typo in the id, and it fails the same silent way an
	// orphan does: the consumer keeps taking the feed's copy.
	for _, m := range members {
		pub, ok := publishing[m]
		if !ok {
			continue
		}
		ids := make([]string, 0, len(pub.As))
		for orig := range pub.As {
			ids = append(ids, orig)
		}
		sort.Strings(ids)
		for _, orig := range ids {
			if !aliasUsed[m][orig] {
				notes = append(notes, fmt.Sprintf("%s says it publishes %s as %s, but produces no package called %s", m, orig, pub.As[orig], orig))
			}
		}
	}
	return out, orphans, notes
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
	// Via is the id the producing project declares when Package is the one it
	// is republished under; "" when they are the same.
	Via string
}

// describe names the link the way someone looking at their own stackspace
// thinks of it: which of my repos depends on which.
func (l stackLink) describe() string {
	pkg := l.Package
	if l.Via != "" {
		pkg = fmt.Sprintf("%s (%s, republished)", l.Package, l.Via)
	}
	return fmt.Sprintf("%s  %s → %s", pkg, strings.Join(l.From, ", "), l.To)
}

// stackReportNotes prints what stackRedirects noticed beyond links and
// orphans, one line each.
func stackReportNotes(out io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintf(out, "· %s\n", n)
	}
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
func stackCheckOverlay(ctx context.Context, root string, m *stackManifest) ([]stackOverlayReport, []stackOrphan, []string) {
	var out []stackOverlayReport
	members, writable := m.names(), m.ownedNames()
	byEco, orphans, notes := stackRedirects(ctx, root, members, m.publishing())
	for _, eco := range ecosystem.Default().All() {
		links := byEco[eco.Info().ID]
		if len(links) == 0 {
			continue
		}
		resp, err := eco.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirectsOf(links), Writable: writable,
		})
		if err != nil || resp.Skipped {
			continue
		}
		out = append(out, stackOverlayReport{Eco: eco.Info().ID, Links: links, Resp: resp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Eco < out[j].Eco })
	return out, orphans, notes
}

// stackEcosystems is the adapters an overlay can be written for, in a stable
// order so output and files come out the same run after run.
func stackEcosystems() []plugin.Ecosystem { return ecosystem.Default().All() }

// localOverlayRequest is the request wire and rm send an adapter: the
// stackspace root, the redirects it owns, and which members' files it may
// patch.
func localOverlayRequest(root string, links []stackLink, writable []string) plugin.LocalOverlayRequest {
	return plugin.LocalOverlayRequest{Root: root, Redirects: redirectsOf(links), Write: true, Writable: writable}
}
