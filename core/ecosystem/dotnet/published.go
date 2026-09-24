package dotnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// Published asks the feed whether the version exists, through NuGet's
// package base address (the "flat container"): GET <base>/<id>/index.json
// lists every version of a package, and a 404 means the package has none. Any
// other failure is an error rather than "not published". A package that isn't
// packable has no registry.
func (a *Adapter) Published(ctx context.Context, req plugin.PublishedRequest) (plugin.PublishedResponse, error) {
	if req.Package.Private {
		return plugin.PublishedResponse{NoRegistry: true}, nil
	}
	what := req.Package.Name + "@" + req.Package.Version
	base, err := packageBaseAddress(ctx, req.PackageSource)
	if err != nil {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: %w", what, err)
	}
	var index struct {
		Versions *[]string `json:"versions"`
	}
	found, err := getJSON(ctx, base+strings.ToLower(req.Package.Name)+"/index.json", &index)
	if err != nil {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: %w", what, err)
	}
	if !found {
		return plugin.PublishedResponse{}, nil
	}
	// A 200 without the list isn't an answer.
	if index.Versions == nil {
		return plugin.PublishedResponse{}, fmt.Errorf("checking %s on the feed: its version index has no versions list", what)
	}
	want := normalizeNuGetVersion(req.Package.Version)
	for _, v := range *index.Versions {
		if normalizeNuGetVersion(v) == want {
			return plugin.PublishedResponse{Published: true}, nil
		}
	}
	return plugin.PublishedResponse{}, nil
}

// normalizeNuGetVersion is NuGet's normalized form, which a feed lists:
// build metadata dropped, leading zeros dropped from the numbers, a fourth
// number dropped when it's zero, at least three numbers, and lowercase.
// "01.0.0.0-Beta+abc" and "1.0.0-beta" are the same version.
func normalizeNuGetVersion(v string) string {
	v, _, _ = strings.Cut(v, "+")
	release, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(release, ".")
	for i, p := range parts {
		if trimmed := strings.TrimLeft(p, "0"); trimmed != "" {
			parts[i] = trimmed
		} else {
			parts[i] = "0"
		}
	}
	if len(parts) == 4 && parts[3] == "0" {
		parts = parts[:3]
	}
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	out := strings.Join(parts, ".")
	if hasPre {
		out += "-" + pre
	}
	return strings.ToLower(out)
}

// nugetOrgBase is nuget.org's package base address.
const nugetOrgBase = "https://api.nuget.org/v3-flatcontainer/"

// packageBaseAddress resolves a package source to its flat-container base
// (with a trailing slash): nuget.org for the default names, else the
// PackageBaseAddress resource the source's v3 service index names. A source
// given by its NuGet.config name can't be resolved from here.
func packageBaseAddress(ctx context.Context, source string) (string, error) {
	switch {
	case source == "" || strings.EqualFold(source, "nuget") || strings.EqualFold(source, "nuget.org"):
		return nugetOrgBase, nil
	case !strings.HasPrefix(source, "http"):
		return "", fmt.Errorf("can't look up versions on the named source %q: give its v3 service index URL as the package source", source)
	}
	var index struct {
		Resources []struct {
			ID   string `json:"@id"`
			Type string `json:"@type"`
		} `json:"resources"`
	}
	found, err := getJSON(ctx, source, &index)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s: no v3 service index there", source)
	}
	for _, r := range index.Resources {
		if strings.HasPrefix(r.Type, "PackageBaseAddress/3.0.0") {
			return strings.TrimSuffix(r.ID, "/") + "/", nil
		}
	}
	return "", fmt.Errorf("%s: the service index names no PackageBaseAddress", source)
}

// registryHTTP is the client for feed queries: a variable so a test can
// shorten its timeout.
var registryHTTP = &http.Client{Timeout: 30 * time.Second}

// getJSON GETs url into dst. It reports false, with no error, for a 404; any
// other non-200 is an error.
func getJSON(ctx context.Context, url string, dst any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := registryHTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return false, fmt.Errorf("%s: %w", url, err)
		}
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("%s: %s %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
}
