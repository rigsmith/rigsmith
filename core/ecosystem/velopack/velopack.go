// Package velopack implements the Velopack desktop-app ecosystem adapter. A
// Velopack app is a project that ships not as a registry package but as signed,
// self-updating installers (a macOS .dmg, a Windows Setup.exe) plus per-channel
// update feeds, built with the `vpk` CLI and attached to a forge release.
//
// Velopack is NOT .NET-only: `vpk pack` wraps any directory of built binaries, so
// it packages C#/.NET, Rust, Go, and Electron apps alike. This adapter therefore
// OVERLAYS whichever base language adapter owns the project beside a velopack file
// (see EcosystemInfo.Overlays and the discovery reconciliation in
// commands.Workspace.Discover): it claims the .csproj / Cargo.toml / package.json
// / go.mod sitting next to a velopack.json — exactly how Tauri claims the crate
// next to a tauri.conf.json — and owns that unit's build, while the base adapter
// keeps owning ordinary library projects.
//
// The version source of truth stays the base manifest: discovery and SetVersion
// delegate to the embedded base adapter for the unit's directory (resolved from
// the velopack file's `base`, or auto-detected from the sibling manifest), so each
// ecosystem's version/lockstep handling is reused verbatim. The velopack file
// supplies only the packaging knobs (pack id, channels, icons, signing) `vpk pack`
// needs, plus — for non-dotnet bases — a `build` command that produces the
// directory of binaries to pack.
package velopack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem/cargo"
	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/ecosystem/gomod"
	"github.com/rigsmith/rigsmith/core/ecosystem/node"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Adapter is the in-process Velopack ecosystem adapter. It embeds the base
// language adapters it can overlay (dotnet, cargo, node, go) and delegates
// discovery and version stamping to whichever one owns a given app's directory —
// a Velopack project's version lives and moves exactly like any other project's
// in its language.
type Adapter struct {
	bases map[string]plugin.Ecosystem
}

// New returns a Velopack adapter wired with the base adapters it overlays. The
// bases are keyed by their EcosystemInfo.ID (gomod reports "go").
func New() *Adapter {
	a := &Adapter{bases: map[string]plugin.Ecosystem{}}
	for _, e := range []plugin.Ecosystem{dotnet.New(), cargo.New(), node.New(), gomod.New()} {
		a.bases[e.Info().ID] = e
	}
	return a
}

var _ plugin.Ecosystem = (*Adapter)(nil)

// baseOrder is the deterministic order velopack consults its base adapters during
// discovery (one app dir has a single manifest type, so order only fixes the rare
// ambiguity and keeps results stable).
var baseOrder = []string{baseDotnet, baseCargo, baseNode, baseGo}

// configNames are the Velopack config filenames this adapter recognizes next to
// a project's manifest: a project is Velopack-owned iff one is present.
var configNames = map[string]bool{
	"velopack.json":  true,
	"velopack.jsonc": true,
}

// Info returns the Velopack adapter's identity and capabilities. It overlays the
// language adapters: a project beside a velopack file is released as a desktop app,
// not a registry package. Publish is intentionally absent — a Velopack app ships
// via git tag + forge release (Artifacts builds the installers and update feeds),
// so there is no registry push.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "velopack",
		DisplayName:      "Velopack",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{"velopack.json", "velopack.jsonc"},
		Overlays:         baseOrder,
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

// Discover returns one Package per project that has a sibling velopack file. It
// asks each base adapter to discover its projects and keeps those a velopack file
// marks as desktop apps, delegating the package's name, version, version file, and
// dependencies to that base's own logic. The kept packages carry the same Dir the
// base assigned, so the overlay reconciliation drops the base's plain package for
// that directory and the app is released once — by Velopack. A velopack file may
// pin its `base` to disambiguate a directory more than one base could claim.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	var resp plugin.DiscoverResponse
	seen := map[string]bool{}
	for _, baseID := range baseOrder {
		bresp, err := a.bases[baseID].Discover(ctx, req)
		if err != nil {
			return plugin.DiscoverResponse{}, err
		}
		for _, pkg := range bresp.Packages {
			if seen[pkg.Dir] || !hasVelopackConfig(filepath.Join(req.RepoRoot, pkg.Dir)) {
				continue
			}
			cfg, err := loadConfig(filepath.Join(req.RepoRoot, pkg.Dir))
			if err != nil {
				return plugin.DiscoverResponse{}, err
			}
			if cfg.Base != "" && cfg.Base != baseID {
				continue // velopack file pins a different base for this directory
			}
			seen[pkg.Dir] = true
			resp.Packages = append(resp.Packages, pkg)
		}
	}
	return resp, nil
}

// SetVersion stamps the new version into the app's version file by delegating to
// the base adapter that owns its directory (its manifest — csproj/Directory.Build.
// props, Cargo.toml, package.json, or go.mod — is rewritten format-preservingly).
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	base, err := a.resolveBase(filepath.Join(req.RepoRoot, req.Package.Dir))
	if err != nil {
		return err
	}
	return base.SetVersion(ctx, req)
}

// resolveBase returns the base adapter for the velopack app in dir: the explicit
// `base` from its velopack file when set, otherwise auto-detected from the sibling
// manifest. It errors when neither yields a known base (a base-less app, not yet
// supported).
func (a *Adapter) resolveBase(dir string) (plugin.Ecosystem, error) {
	id := ""
	if cfg, err := loadConfig(dir); err == nil {
		id = cfg.Base
	}
	if id == "" {
		id = detectBase(dir)
	}
	base, ok := a.bases[id]
	if !ok {
		return nil, fmt.Errorf("velopack: cannot determine the base ecosystem for %s — add a sibling manifest (.csproj/Cargo.toml/package.json/go.mod) or set \"base\" in the velopack file", dir)
	}
	return base, nil
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
			"requires the Velopack CLI (`vpk`) on PATH; configure packaging in velopack.json next to the project manifest (.csproj/Cargo.toml/package.json/go.mod)",
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
