package cli

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/core/semver"
)

type stackReleaseDependency struct {
	ID      string `xml:"id,attr"`
	Version string `xml:"version,attr"`
	Exclude string `xml:"exclude,attr"`
	Include string `xml:"include,attr"`
}
type stackReleaseDependencies struct {
	Direct []stackReleaseDependency `xml:"dependency"`
	Groups []struct {
		Framework    string                   `xml:"targetFramework,attr"`
		Dependencies []stackReleaseDependency `xml:"dependency"`
	} `xml:"group"`
}

// NuGet bare versions are inclusive minima, unlike npm exact-version syntax.
// Unknown/floating/unbounded-lower ranges cannot prove a minimum.
func stackReleaseMinimum(raw string) (semver.Version, bool) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "(") {
		if len(s) < 3 || (s[len(s)-1] != ']' && s[len(s)-1] != ')') {
			return semver.Version{}, false
		}
		body := s[1 : len(s)-1]
		parts := strings.Split(body, ",")
		if len(parts) == 1 {
			if s[0] != '[' || s[len(s)-1] != ']' {
				return semver.Version{}, false
			}
			return semver.Parse(strings.TrimSpace(body))
		}
		if len(parts) != 2 {
			return semver.Version{}, false
		}
		low, ok := semver.Parse(strings.TrimSpace(parts[0]))
		if !ok {
			return low, false
		}
		upper := strings.TrimSpace(parts[1])
		if upper != "" {
			high, ok := semver.Parse(upper)
			if !ok || semver.Compare(low, high) > 0 || semver.Compare(low, high) == 0 && (s[0] != '[' || s[len(s)-1] != ']') {
				return low, false
			}
		}
		return low, true
	}
	return semver.Parse(s)
}

func (d stackReleaseDependencies) delivers(pkg, version string) bool {
	needed, ok := semver.Parse(version)
	if !ok {
		return false
	}
	check := func(deps []stackReleaseDependency) bool {
		found := false
		for _, dep := range deps {
			if strings.EqualFold(dep.ID, pkg) {
				assets := func(raw string) []string {
					return strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
				}
				excluded := assets(dep.Exclude)
				included := assets(dep.Include)
				if slices.Contains(excluded, "compile") || slices.Contains(excluded, "all") || (len(included) > 0 && !slices.Contains(included, "compile") && !slices.Contains(included, "all")) {
					return false
				}
				low, ok := stackReleaseMinimum(dep.Version)
				if !ok || semver.Compare(low, needed) < 0 {
					return false
				}
				found = true
			}
		}
		return found
	}
	if len(d.Groups) == 0 {
		return check(d.Direct)
	}
	// Every group must deliver it: choosing a framework from project text is
	// insufficient for imported/multitargeted project configurations.
	for _, g := range d.Groups {
		if !check(append(slices.Clone(d.Direct), g.Dependencies...)) {
			return false
		}
	}
	return true
}

func stackReleaseAllLinks(ctx context.Context, root string, m *stackManifest) ([]stackLink, error) {
	byEco, _, _, failed := stackRedirects(ctx, root, m.names(), m.publishing())
	if len(failed) > 0 {
		return nil, fmt.Errorf("can't check package ownership: discovery failed")
	}
	var links []stackLink
	for _, ls := range byEco {
		links = append(links, ls...)
	}
	return links, nil
}

// Return the direct package pin to edit. Guard identity always stays pkg.
func stackReleaseDelivery(ctx context.Context, a stackDepsAPI, links []stackLink, member, sibling, pkg, version, via, viaVersion, feed string) (string, string, string, error) {
	owns := false
	var candidates []stackLink
	for _, l := range links {
		if l.To == sibling && strings.EqualFold(l.Package, pkg) {
			owns = true
		}
		if !slices.Contains(l.From, member) {
			continue
		}
		if l.To == sibling && strings.EqualFold(l.Package, pkg) {
			return pkg, version, "direct reference", nil
		}
		if slices.ContainsFunc(l.Reachable, func(id string) bool { return strings.EqualFold(id, pkg) }) && (via == "" || strings.EqualFold(via, l.Package)) {
			candidates = append(candidates, l)
		}
	}
	if !owns {
		return "", "", "", fmt.Errorf("package is not produced by the selected sibling")
	}
	if len(candidates) != 1 {
		return "", "", "", fmt.Errorf("need one direct delivery package; select --via-package (or add a direct reference to %s >= %s manually); guards unchanged", pkg, version)
	}
	control := candidates[0]
	versions := []string{viaVersion}
	base := strings.TrimRight(feed, "/") + "/" + url.PathEscape(strings.ToLower(control.Package))
	if viaVersion == "" {
		var index struct {
			Versions []string `json:"versions"`
		}
		if err := stackReleaseGet(ctx, a.client, base+"/index.json", &index, false); err != nil {
			return "", "", "", err
		}
		versions = nil
		for i := len(index.Versions) - 1; i >= 0 && len(versions) < 20; i-- {
			if !strings.Contains(index.Versions[i], "-") {
				versions = append(versions, index.Versions[i])
			}
		}
	}
	for _, v := range versions {
		if !stackReleaseFixedVersion.MatchString(v) {
			return "", "", "", fmt.Errorf("invalid delivery version")
		}
		var spec struct {
			Metadata struct {
				Dependencies stackReleaseDependencies `xml:"dependencies"`
			} `xml:"metadata"`
		}
		if err := stackReleaseGet(ctx, a.client, base+"/"+url.PathEscape(strings.ToLower(v))+"/"+url.PathEscape(strings.ToLower(control.Package))+".nuspec", &spec, true); err != nil {
			return "", "", "", err
		}
		if spec.Metadata.Dependencies.delivers(pkg, version) {
			return control.Package, v, fmt.Sprintf("delivery: %s %s requires %s >= %s in every dependency group; migrate/publish %s first, then %s, then %s; only %s guards are removed", control.Package, v, pkg, version, sibling, control.To, member, pkg), nil
		}
	}
	return "", "", "", fmt.Errorf("guards unchanged: no checked %s release delivers %s >= %s in every framework group; migrate/publish %s first, then publish %s with that dependency minimum, then rerun for %s; alternatively add a direct %s reference >= %s manually", control.Package, pkg, version, sibling, control.To, member, pkg, version)
}
