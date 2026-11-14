package commands

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/auth"
	"github.com/rigsmith/rigsmith/core/config"
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
//
// Packages sharing a version file with one named here (lockstep, unless
// independent says otherwise) are released with it: they carry the same
// version, and `publish` would otherwise ship them with no changelog entry.
// A package given both its current version and another is refused, since
// splitting the specs here would keep applyReleaseAs from seeing the clash.
func initialReleases(ctx context.Context, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string, specs []string, independent bool) (map[string]string, []string, error) {
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
		if err := checkUnreleased(ctx, ws, pkgs, ecoOf, p, spec); err != nil {
			return nil, nil, err
		}
		initial[p.Name] = v.String()
	}

	for _, spec := range rest {
		name, version, named := strings.Cut(spec, "=")
		name = strings.TrimSpace(name)
		if !named && len(releasable) == 1 {
			name, version = releasable[0].Name, spec
		}
		if first, ok := initial[name]; ok {
			return nil, nil, fmt.Errorf("--release-as gives %s two versions, %s (its first release) and %s: give it one", name, first, strings.TrimSpace(version))
		}
	}

	for _, name := range sortedKeys(initial) {
		p := byName[name]
		if p.VersionFile == "" || independent || ecoStrategy(ws.Config, ecoOf[name]) == config.Independent {
			continue
		}
		for _, sib := range releasable {
			if _, done := initial[sib.Name]; done || sib.VersionFile != p.VersionFile || ecoOf[sib.Name] != ecoOf[name] {
				continue
			}
			spec := sib.Name + "=" + initial[name] + " (sharing " + p.VersionFile + " with " + name + ")"
			if sib.Version != p.Version {
				return nil, nil, fmt.Errorf("--release-as %s: it is at %s, not %s, so the shared version file disagrees with itself", spec, sib.Version, p.Version)
			}
			if err := checkUnreleased(ctx, ws, pkgs, ecoOf, sib, spec); err != nil {
				return nil, nil, err
			}
			initial[sib.Name] = initial[name]
		}
	}
	return initial, rest, nil
}

// checkUnreleased refuses a first release of p when its current version is
// already tagged or published, or when that can't be found out.
func checkUnreleased(ctx context.Context, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string, p plugin.Package, spec string) error {
	where, err := releasedAt(ctx, ws, pkgs, ecoOf, p)
	if err != nil {
		return fmt.Errorf("--release-as %s: %w", spec, err)
	}
	if where != "" {
		return fmt.Errorf("--release-as %s: %s %s is already released (%s); --release-as at the current version is only for a first release of a version never published or tagged. "+
			"To release again, give it a changeset, or --release-as a higher version", spec, p.Name, p.Version, where)
	}
	return nil
}

// ecoStrategy is the version strategy a package in ecosystem eco follows: its
// block's versionStrategy, else the top-level one.
func ecoStrategy(cfg *config.Config, eco string) config.VersionStrategy {
	if s := cfg.EcoStrategy(eco); s != "" {
		return s
	}
	return cfg.VersionStrategy
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// releasedAt says where a package's current version has already been
// released — "tag <name>" or "on its registry" — or "" when nowhere. A tag
// counts locally or on the default remote; the registry is asked through the
// package's ecosystem, and a private package, or an ecosystem released by its
// tag alone, has none. A remote or registry that can't answer is an error: an
// initial release needs to know.
//
// A variable so a test can stand in for the registry.
var releasedAt = func(ctx context.Context, ws *Workspace, pkgs []plugin.Package, ecoOf map[string]string, p plugin.Package) (string, error) {
	ecoID := ecoOf[p.Name]
	tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoID, p.Dir, p.Name, p.Version, len(pkgs) == 1)
	if gitutil.TagExists(ctx, ws.Root, tag) {
		return "tag " + tag, nil
	}
	if remote := gitutil.DefaultRemote(ctx, ws.Root); remote != "" {
		// A remote that can't be asked is no proof the tag isn't there: a
		// shallow checkout may have left the tag behind on it.
		found, err := gitutil.LookupRemoteTag(ctx, ws.Root, remote, tag)
		if err != nil {
			return "", fmt.Errorf("couldn't check %s for tag %s, which a first release has to know: %w", remote, tag, err)
		}
		if found {
			return "tag " + tag + " on " + remote, nil
		}
	}
	if p.Private {
		return "", nil
	}
	eco, ok := ws.EcosystemFor(ecoID)
	if !ok {
		return "", nil
	}
	resp, err := eco.Published(ctx, publishedRequest(ctx, ws, ecoID, p))
	if err != nil {
		return "", fmt.Errorf("couldn't tell whether %s@%s is on its registry, which a first release has to know: %w", p.Name, p.Version, err)
	}
	if resp.Published {
		return "on its registry", nil
	}
	return "", nil
}

// publishedRequest is the registry lookup for p, carrying the ecosystem's
// configured publish credential (`auth`) as publish-plan's does, for a feed
// that won't answer an anonymous read: the adapter sends it only when the
// registry asks. A credential that can't be resolved is marked unavailable,
// with a reason that holds no secret, so the adapter doesn't substitute an
// ambient one.
func publishedRequest(ctx context.Context, ws *Workspace, ecoID string, p plugin.Package) plugin.PublishedRequest {
	ec := ws.Config.EcoConfig(ecoID)
	req := plugin.PublishedRequest{RepoRoot: ws.Root, Package: p, PackageSource: ec.PackageSource, User: ec.User}
	if ref := strings.TrimSpace(ec.Auth); ref != "" {
		cred, err := auth.Resolve(ctx, auth.Request{Ref: ref})
		switch {
		case err != nil:
			req.AuthUnavailable, req.AuthError = true, auth.SafeReason(ref, err)
		case cred.Resolved():
			req.Auth = &plugin.AuthCredential{Token: cred.Token, Method: string(cred.Method)}
		}
	}
	return req
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
