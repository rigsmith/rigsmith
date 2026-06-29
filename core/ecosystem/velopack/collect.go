package velopack

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// collectReleases classifies the files Velopack wrote into dir into release
// artifacts. Everything the user installs OR the app auto-updates from is attached
// to the forge release; build-only metadata is not.
//
//   - First-install installers — the macOS .dmg and the Windows *-Setup.exe —
//     attach so a user can download one to install.
//   - The update feed — the per-channel releases.<channel>.json index and the
//     *-full.nupkg / *-delta.nupkg payloads — also attach. Velopack's in-app
//     updater (GithubSource) finds an update purely by listing a release's assets
//     over the GitHub REST API: it fetches the asset literally named
//     releases.<channel>.json, then the .nupkg assets named in it. So uploading
//     these as ordinary release assets (what the generic forge step does) is a
//     complete, working feed — no `vpk upload` required. The index and any deltas
//     are produced at `vpk pack` time, so they are already on disk here.
//   - The legacy RELEASES-<channel> file (only emitted for a default channel and
//     unused by the modern updater) and the assets.<channel>.json build manifest
//     are collected for visibility but NOT attached.
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
	case strings.HasSuffix(lower, "setup.exe"):
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactBinary, Attach: true}, true
	case strings.HasSuffix(lower, ".nupkg"):
		// Full and delta update payloads — the updater downloads these by the
		// filenames listed in releases.<channel>.json.
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactPackage, Attach: true}, true
	case strings.HasPrefix(lower, "releases.") && strings.HasSuffix(lower, ".json"):
		// The per-channel feed index the in-app updater fetches by name.
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactPackage, Attach: true}, true
	case strings.HasPrefix(lower, "assets.") && strings.HasSuffix(lower, ".json"),
		strings.HasPrefix(lower, "releases-"):
		// Build manifest (assets.<ch>.json) and the legacy RELEASES-<ch> file:
		// unused by the modern updater, so collected but not attached.
		return plugin.Artifact{Path: path, Kind: plugin.ArtifactPackage, Attach: false}, true
	default:
		return plugin.Artifact{}, false
	}
}
