package cli

import (
	"context"
	"path/filepath"
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
func stackRedirects(ctx context.Context, root string, members []string) map[string][]plugin.Redirect {
	member := func(rel string) string {
		rel = filepath.ToSlash(rel)
		for _, m := range members {
			if rel == m || strings.HasPrefix(rel, m+"/") {
				return m
			}
		}
		return ""
	}

	out := map[string][]plugin.Redirect{}
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
		}
		seen := map[string]bool{}
		var redirects []plugin.Redirect
		for _, p := range resp.Packages {
			from := member(p.Dir)
			for _, d := range p.Dependencies {
				// Only a registry-referenced sibling needs redirecting: a project
				// reference already resolves to the sources beside it.
				if !d.ViaRegistry {
					continue
				}
				prod, ok := producer[d.Name]
				if !ok || seen[d.Name] {
					continue
				}
				// Within one member the projects already reference each other
				// directly; only a reference that leaves its member goes through
				// a registry and needs redirecting.
				if to := member(prod.Dir); to == "" || to == from {
					continue
				}
				seen[d.Name] = true
				redirects = append(redirects, plugin.Redirect{
					Package: d.Name,
					Path:    filepath.ToSlash(prod.ManifestPath),
				})
			}
		}
		if len(redirects) > 0 {
			sort.Slice(redirects, func(i, j int) bool { return redirects[i].Package < redirects[j].Package })
			out[eco.Info().ID] = redirects
		}
	}
	return out
}

// stackOverlayReport is one ecosystem's answer about the build wiring.
type stackOverlayReport struct {
	Eco       string
	Redirects []plugin.Redirect
	Resp      plugin.LocalOverlayResponse
}

// stackCheckOverlay asks each ecosystem whether the redirects it needs are in
// effect, without changing anything.
func stackCheckOverlay(ctx context.Context, root string, members []string) []stackOverlayReport {
	var out []stackOverlayReport
	byEco := stackRedirects(ctx, root, members)
	for _, eco := range ecosystem.Default().All() {
		redirects := byEco[eco.Info().ID]
		if len(redirects) == 0 {
			continue
		}
		resp, err := eco.LocalOverlay(ctx, plugin.LocalOverlayRequest{
			Root: root, Redirects: redirects,
		})
		if err != nil || resp.Skipped {
			continue
		}
		out = append(out, stackOverlayReport{Eco: eco.Info().ID, Redirects: redirects, Resp: resp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Eco < out[j].Eco })
	return out
}
