package velopack

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// collectReleases classifies the files Velopack wrote into dir into release
// artifacts. Two groups:
//
//   - First-install installers — the macOS .dmg and the Windows *-Setup.exe — are
//     plain release assets (Attach: true): a user downloads one to install.
//   - The update feed — the *-full.nupkg packages and their releases.*.json /
//     RELEASES-* / assets.*.json metadata — are NOT attached as loose assets
//     (Attach: false): they are uploaded as a coherent set by `vpk upload`, which
//     lays them out so the in-app updater can find them. Returning them (un-
//     attached) keeps them visible in the build output without the generic forge
//     step scattering them.
//
// Results are sorted by path for a stable order.
func collectReleases(dir string) []plugin.Artifact {
	var arts []plugin.Artifact
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if art, ok := classify(path, d.Name()); ok {
			arts = append(arts, art)
		}
		return nil
	})
	sort.Slice(arts, func(i, j int) bool { return arts[i].Path < arts[j].Path })
	return arts
}

// classify maps one output filename to an Artifact, or reports ok=false for files
// that are not release artifacts (e.g. an intermediate trustedsigning.json).
func classify(path, name string) (plugin.Artifact, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".dmg"):
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactBinary, Attach: true}, true
	case strings.HasSuffix(lower, "-setup.exe"), strings.HasSuffix(lower, "setup.exe"):
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactBinary, Attach: true}, true
	case strings.HasSuffix(lower, ".nupkg"):
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactPackage, Attach: false}, true
	case strings.HasPrefix(lower, "releases.") && strings.HasSuffix(lower, ".json"),
		strings.HasPrefix(lower, "assets.") && strings.HasSuffix(lower, ".json"),
		strings.HasPrefix(lower, "releases-"), strings.HasPrefix(lower, "releases."):
		// Velopack feed metadata: releases.<ch>.json, assets.<ch>.json, RELEASES-<ch>.
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactPackage, Attach: false}, true
	default:
		return plugin.Artifact{}, false
	}
}
