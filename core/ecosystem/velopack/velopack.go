// Package velopack implements the Velopack desktop-app ecosystem adapter. A
// Velopack app is an ordinary .NET project (.csproj) that ships not as a NuGet
// package but as signed, self-updating installers (a macOS .dmg, a Windows
// Setup.exe) plus per-channel update feeds, built with the `vpk` CLI and
// attached to a forge release.
//
// So this adapter OVERLAYS the dotnet adapter (see EcosystemInfo.Overlays and
// the discovery reconciliation in commands.Workspace.Discover): it claims the
// .csproj sitting next to a velopack.json — exactly how Tauri claims the crate
// next to a tauri.conf.json — and owns that unit's build, while plain dotnet
// keeps owning ordinary library projects (which it packs and pushes to NuGet).
//
// The version source of truth stays the csproj (or its shared
// Directory.Build.props): discovery and SetVersion delegate to the embedded
// dotnet adapter, so all of .NET's version/props/lockstep handling is reused
// verbatim. velopack.json supplies only the packaging knobs (pack id, channels,
// icons, signing identities) that `vpk pack` needs — the settings the original
// hand-rolled pack.sh hard-coded.
package velopack

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Adapter is the in-process Velopack ecosystem adapter. It embeds a dotnet
// adapter to reuse .NET's discovery, version resolution, and version stamping —
// a Velopack project is a .csproj, so its version lives and moves exactly like
// any other .NET project's.
type Adapter struct {
	dotnet *dotnet.Adapter
}

// New returns a Velopack adapter.
func New() *Adapter { return &Adapter{dotnet: dotnet.New()} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// configNames are the Velopack config filenames this adapter recognizes next to
// a project's .csproj: a project is Velopack-owned iff one is present.
var configNames = map[string]bool{
	"velopack.json":  true,
	"velopack.jsonc": true,
}

// Info returns the Velopack adapter's identity and capabilities. It overlays
// dotnet: a .csproj beside a velopack.json is released as a desktop app, not a
// NuGet package. Publish is intentionally absent — a Velopack app ships via git
// tag + forge release (Artifacts builds the installers and update feeds), so
// there is no registry push.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "velopack",
		DisplayName:      "Velopack",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{"velopack.json", "velopack.jsonc"},
		Overlays:         []string{"dotnet"},
	}
}

// Detect reports whether any Velopack-owned project exists under root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// Discover returns one Package per .csproj that has a sibling velopack.json. It
// delegates to the dotnet adapter (so the package's name, version, version file,
// and dependencies are resolved by .NET's own logic) and keeps only the projects
// a velopack.json marks as Velopack apps. The kept packages carry the same Dir
// the dotnet adapter assigned, so the overlay reconciliation drops the plain
// dotnet package for that directory and the app is released once — by Velopack.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	base, err := a.dotnet.Discover(ctx, req)
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}
	var resp plugin.DiscoverResponse
	for _, pkg := range base.Packages {
		if hasVelopackConfig(filepath.Join(req.RepoRoot, pkg.Dir)) {
			resp.Packages = append(resp.Packages, pkg)
		}
	}
	return resp, nil
}

// SetVersion stamps the new version into the project's version file, delegating
// to the dotnet adapter (the version lives in the csproj or a shared
// Directory.Build.props, format-preservingly rewritten).
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	return a.dotnet.SetVersion(ctx, req)
}

// Publish is a no-op: a Velopack app is released by the tagging phase and the
// forge release (Artifacts builds the installers + feeds to attach/upload), not
// pushed to a registry. (Not advertised in Capabilities.)
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "Velopack app: released via git tag + forge release"}, nil
}

// ReleaseInit declares Velopack's release prerequisites: the `vpk` CLI (and, for
// Windows Azure Trusted Signing from a non-Windows host, a JDK), the forge token
// the release already preflights, and the note that code-signing/notarization
// secrets are only needed when the velopack.json enables them.
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	return plugin.ReleaseInitResponse{
		Notes: []string{
			"Velopack app: installers + update feeds are built with `vpk pack` and attached to the forge release (no registry push)",
			"requires the Velopack CLI (`vpk`) on PATH; configure packaging in velopack.json next to the .csproj",
			"macOS signing/notarization and Windows Azure Trusted Signing are optional — enable them in velopack.json; secrets ride in via the signing config",
		},
	}, nil
}

// hasVelopackConfig reports whether dir contains a velopack.json/velopack.jsonc.
func hasVelopackConfig(dir string) bool {
	for name := range configNames {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// velopackRefRe matches a <PackageReference Include="Velopack" ... /> element
// (case-insensitive on the package id, attribute order independent) so the
// referenced Velopack major can be read for the CLI compatibility check.
var velopackRefRe = regexp.MustCompile(`(?i)<PackageReference\b[^>]*\bInclude\s*=\s*"Velopack"[^>]*>`)

// velopackVersionAttrRe pulls a Version="x.y.z" attribute out of a single element.
var velopackVersionAttrRe = regexp.MustCompile(`(?i)\bVersion\s*=\s*"([^"]+)"`)

// velopackRefVersion returns the version of the Velopack <PackageReference> in a
// csproj's text, or "" when the project does not reference Velopack (or pins it
// elsewhere, e.g. central package management). Used only for the CLI-major
// compatibility check, which is skipped when this is empty.
func velopackRefVersion(csprojText string) string {
	elem := velopackRefRe.FindString(csprojText)
	if elem == "" {
		return ""
	}
	if m := velopackVersionAttrRe.FindStringSubmatch(elem); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
