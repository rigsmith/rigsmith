package commands

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/semver"
)

// DefaultInitialMessage is the changelog line of an initial release that has
// no changesets of its own and no --initial-message.
const DefaultInitialMessage = "Initial release"

// initialReleases picks out the --release-as specs that name the version a
// package is already at: the release of a version that was written into the
// manifest but never published (a new package, set up at 0.1.0). Each must be
// unreleased — no git tag for it and not on its registry — or it's refused,
// since releasing it again would rewrite the changelog of something that
// shipped. It returns those packages with their version, and the specs left
// for applyReleaseAs, which handles every move upward.
//
// A bare <version> counts only when the workspace has one package to release:
// with nothing releasing yet, there is no other way to tell which it names.
func initialReleases(ctx context.Context, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string, specs []string) (map[string]string, []string, error) {
	byName := make(map[string]plugin.Package, len(pkgs))
	var releasable []plugin.Package
	for _, p := range pkgs {
		byName[p.Name] = p
		if !ws.Config.IsIgnored(p.Name) {
			releasable = append(releasable, p)
		}
	}
	initial := map[string]string{}
	var rest []string
	for _, spec := range specs {
		name, version, named := strings.Cut(spec, "=")
		name = strings.TrimSpace(name)
		var p plugin.Package
		switch {
		case named:
			var ok bool
			if p, ok = byName[name]; !ok {
				rest = append(rest, spec)
				continue
			}
		case len(releasable) == 1:
			version, p = name, releasable[0]
		default:
			rest = append(rest, spec)
			continue
		}
		v, ok := semver.Parse(strings.TrimSpace(version))
		cur, curOK := semver.Parse(p.Version)
		if !ok || !curOK || semver.Compare(v, cur) != 0 {
			rest = append(rest, spec)
			continue
		}
		if ws.Config.IsIgnored(p.Name) {
			return nil, nil, fmt.Errorf("--release-as %s: %s is ignored, so it isn't released", spec, p.Name)
		}
		where, err := releasedAt(ctx, ws, pkgs, ecoOf, p)
		if err != nil {
			return nil, nil, fmt.Errorf("--release-as %s: %w", spec, err)
		}
		if where != "" {
			return nil, nil, fmt.Errorf("--release-as %s: %s %s is already released (%s); --release-as at the current version is only for a first release of a version never published or tagged. "+
				"To release again, give it a changeset, or --release-as a higher version", spec, p.Name, p.Version, where)
		}
		initial[p.Name] = v.String()
	}
	return initial, rest, nil
}

// releasedAt says where a package's current version has already been
// released — "tag <name>" or "on its registry" — or "" when nowhere. A tag
// counts locally or on the default remote; the registry is asked through the
// package's ecosystem, and a private package, or an ecosystem released by its
// tag alone, has none. A registry that can't answer is an error: an initial
// release needs to know.
//
// A variable so a test can stand in for the registry.
var releasedAt = func(ctx context.Context, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string, p plugin.Package) (string, error) {
	ecoID := ecoOf[p.Name]
	tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoID, p.Dir, p.Name, p.Version, len(pkgs) == 1)
	if gitutil.TagExists(ctx, ws.Root, tag) {
		return "tag " + tag, nil
	}
	if remote := gitutil.DefaultRemote(ctx, ws.Root); remote != "" && gitutil.RemoteTagExists(ctx, ws.Root, remote, tag) {
		return "tag " + tag + " on " + remote, nil
	}
	if p.Private {
		return "", nil
	}
	eco, ok := ws.EcosystemFor(ecoID)
	if !ok {
		return "", nil
	}
	resp, err := eco.Published(ctx, plugin.PublishedRequest{
		RepoRoot:      ws.Root,
		Package:       p,
		PackageSource: ws.Config.EcoConfig(ecoID).PackageSource,
		User:          ws.Config.EcoConfig(ecoID).User,
	})
	if err != nil {
		return "", fmt.Errorf("couldn't tell whether %s@%s is on its registry, which a first release has to know: %w", p.Name, p.Version, err)
	}
	if resp.Published {
		return "on its registry", nil
	}
	return "", nil
}

// applyInitialReleases puts each initial release into the plan at the version
// it is already at. A package its changesets already planned keeps their
// changes; one with none gets a module of its own whose single change is
// message. Either way the version isn't bumped, and nothing is cascaded for it.
func applyInitialReleases(out io.Writer, plan []*planner.Module, pkgs []plugin.Package, initial map[string]string, message string) []*planner.Module {
	if strings.TrimSpace(message) == "" {
		message = DefaultInitialMessage
	}
	for _, p := range pkgs {
		version, ok := initial[p.Name]
		if !ok {
			continue
		}
		var m *planner.Module
		for _, pm := range plan {
			if pm.Name == p.Name {
				m = pm
				break
			}
		}
		if m == nil {
			m = planner.InitialRelease(p, message)
			plan = append(plan, m)
		}
		m.VersionOverride = version
		m.ExactVersion = true
		fmt.Fprintln(out, DimStyle.Render(fmt.Sprintf("  initial release %s %s (never published or tagged)", m.DisplayName, version)))
	}
	return plan
}
