// Package electron implements the Electron desktop-app ecosystem adapter. An
// Electron app is a Node package (its version lives in package.json) that is not
// published to npm but distributed as native installers (.dmg/NSIS .exe/.AppImage
// /.deb/…) attached to a forge release. So this adapter overlays the node
// adapter: it claims a package.json that is an Electron app (see
// EcosystemInfo.Overlays and the discovery reconciliation in
// commands.Workspace.Discover) and owns that unit's build, while node continues
// to own ordinary libraries. Versioning is identical to node's (the package.json
// "version"); only distribution differs (electron-builder / electron-forge,
// not npm publish), which is why Electron is a peer adapter rather than a flag on
// node — symmetric with Tauri-over-cargo.
//
// The intra-repo dependency edges the cascade needs are computed by the node
// adapter and handed to this overlay during reconciliation, so this adapter does
// not recompute them.
package electron

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Adapter is the in-process Electron ecosystem adapter.
type Adapter struct{}

// New returns an Electron adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// Info returns the Electron adapter's identity and capabilities. It overlays
// node: an Electron package.json is released as a desktop app, not an npm
// package. Publish is intentionally absent — installers ship via the forge
// release (Artifacts builds them to attach), not a registry.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "electron",
		DisplayName:      "Electron",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{"package.json"},
		Overlays:         []string{"node"},
	}
}

// Detect reports whether any Electron-app package.json exists under root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// Discover walks SourcePath for package.json files that are Electron apps and
// returns one Package each. Dir/Name/ManifestPath match what the node adapter
// would report for the same package.json, so overlay reconciliation drops node's
// duplicate and existing changesets keep resolving.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	var resp plugin.DiscoverResponse
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) != "package.json" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var pj pkgJSON
		if json.Unmarshal(content, &pj) != nil || pj.Name == "" {
			return nil
		}
		if !isElectronApp(filepath.Dir(path), pj) {
			return nil
		}
		resp.Packages = append(resp.Packages, plugin.Package{
			Name:         pj.Name,
			Version:      pj.Version,
			Dir:          relTo(root, filepath.Dir(path)),
			ManifestPath: relTo(root, path),
			Private:      pj.Private,
		})
		return nil
	})
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}
	return resp, nil
}

// SetVersion rewrites the package.json "version" field, format-preserving.
// (Electron's version is plain Node; only its distribution differs.)
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	target := req.Package.VersionFile
	if target == "" {
		target = req.Package.ManifestPath
	}
	path := filepath.Join(req.RepoRoot, target)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(setVersionField(string(content), req.NewVersion)), 0o644)
}

// Publish is a no-op: an Electron app is released by the publish tagging phase and
// the forge release (Artifacts builds the installers to attach), not pushed to a
// registry. (Not advertised in Capabilities. electron-builder's own publisher is
// a possible config-gated follow-up; the default path is shiprig's single forge
// attach.)
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "Electron app: released via git tag + forge release"}, nil
}

// Artifacts runs the detected builder (electron-forge make when a forge config is
// present, else electron-builder) and collects the produced installers so the
// forge-release step can attach them.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	forge := usesForge(dir, readPkg(filepath.Join(dir, "package.json")))
	builder := "electron-builder"
	if forge {
		builder = "electron-forge"
	}

	signed := req.Signing != nil && len(req.Signing.Env) > 0
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would %s build %s@%s%s", builder, req.Package.Name, req.Package.Version, signedSuffix(signed))}, nil
	}

	// Signing secrets (when enabled) ride in via the environment — electron-builder
	// and electron-forge read the standard CSC_*/APPLE_* variables.
	env := mergeSigningEnv(req.BaseEnv(), req.Signing)
	var outDir string
	if forge {
		if _, _, err := runCmdEnv(ctx, dir, env, "npx", "electron-forge", "make"); err != nil {
			return plugin.ArtifactsResponse{}, fmt.Errorf("electron-forge make: %w", err)
		}
		outDir = filepath.Join(dir, "out", "make")
	} else {
		if _, _, err := runCmdEnv(ctx, dir, env, "npx", "electron-builder", "--publish", "never"); err != nil {
			return plugin.ArtifactsResponse{}, fmt.Errorf("electron-builder: %w", err)
		}
		outDir = filepath.Join(dir, "dist")
	}

	arts := collectInstallers(outDir)
	return plugin.ArtifactsResponse{
		Built:     true,
		Artifacts: arts,
		Message:   fmt.Sprintf("built %d installer(s) for %s@%s via %s%s", len(arts), req.Package.Name, req.Package.Version, builder, signedSuffix(signed)),
	}, nil
}

// ReleaseInit declares Electron's release prerequisites: no registry token
// (distribution is the forge release, preflighted by the engine), the builder
// tool on PATH, and a note that signing/notarization is optional and off by
// default (the signing seam declares those secrets, not this adapter).
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	return plugin.ReleaseInitResponse{
		Notes: []string{
			"Electron app: installers are built with electron-builder / electron-forge and attached to the forge release (no npm publish)",
			"requires the builder available via npx (electron-builder, or @electron-forge/cli)",
			"code-signing/notarization is optional and off by default — enable it via the signing config to sign the installers",
		},
	}, nil
}

// --- Electron-app detection ---

// pkgJSON is the subset of package.json this adapter reads. Build and Config.Forge
// are kept raw so their mere presence (electron-builder config in package.json /
// an electron-forge config block) can be detected.
type pkgJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Private         bool              `json:"private"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Build           json.RawMessage   `json:"build"`
	Config          struct {
		Forge json.RawMessage `json:"forge"`
	} `json:"config"`
}

// isElectronApp reports whether a package.json (in dir) is an Electron app:
// it depends on electron, or carries an electron-builder / electron-forge config
// (in package.json or as a sibling config file).
func isElectronApp(dir string, pj pkgJSON) bool {
	if _, ok := pj.Dependencies["electron"]; ok {
		return true
	}
	if _, ok := pj.DevDependencies["electron"]; ok {
		return true
	}
	if len(pj.Build) > 0 { // electron-builder config in package.json
		return true
	}
	return usesForge(dir, pj) || hasBuilderConfigFile(dir)
}

// usesForge reports whether the package uses electron-forge — a "config.forge"
// block in package.json or a forge.config.* file beside it.
func usesForge(dir string, pj pkgJSON) bool {
	if len(pj.Config.Forge) > 0 {
		return true
	}
	for _, name := range []string{"forge.config.js", "forge.config.cjs", "forge.config.mjs", "forge.config.ts"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// hasBuilderConfigFile reports whether an electron-builder config file sits beside
// the package.json.
func hasBuilderConfigFile(dir string) bool {
	for _, name := range []string{
		"electron-builder.yml", "electron-builder.yaml", "electron-builder.json",
		"electron-builder.json5", "electron-builder.toml", "electron-builder.js",
		"electron-builder.cjs", "electron-builder.mjs",
	} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// --- installer collection (testable without a toolchain) ---

// installerExts is the set of Electron installer/asset extensions worth attaching
// to a release. .blockmap and latest*.yml support electron-updater; they are
// attached when present (Decision #5: attach an updater manifest the builder
// produces, don't generate one).
var installerExts = map[string]string{
	".dmg":      plugin.ArtifactBinary,
	".exe":      plugin.ArtifactBinary, // NSIS / Squirrel setup
	".appimage": plugin.ArtifactBinary,
	".deb":      plugin.ArtifactBinary,
	".rpm":      plugin.ArtifactBinary,
	".snap":     plugin.ArtifactBinary,
	".nupkg":    plugin.ArtifactPackage, // Squirrel.Windows
	".zip":      plugin.ArtifactArchive,
	".blockmap": plugin.ArtifactBinary, // electron-updater delta map
}

// collectInstallers walks the builder's output directory and returns one
// Attach:true Artifact per installer/asset, including any latest*.yml updater
// manifest.
func collectInstallers(outDir string) []plugin.Artifact {
	var arts []plugin.Artifact
	_ = filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if isUpdaterManifest(name) {
			arts = append(arts, plugin.Artifact{Path: path, Attach: true})
			return nil
		}
		if kind, ok := installerExts[strings.ToLower(filepath.Ext(name))]; ok {
			arts = append(arts, plugin.Artifact{Path: path, Kind: kind, Attach: true})
		}
		return nil
	})
	return arts
}

// isUpdaterManifest reports whether name is an electron-updater manifest
// (latest.yml, latest-mac.yml, latest-linux.yml, …).
func isUpdaterManifest(name string) bool {
	return strings.HasPrefix(name, "latest") && strings.HasSuffix(name, ".yml")
}

// --- format-preserving version write (mirrors the node adapter) ---

var versionFieldRe = regexp.MustCompile(`("version"\s*:\s*")([^"]*)(")`)

// setVersionField replaces only the first `"version": "..."` value, preserving
// layout — package.json's top-level "version" precedes any nested occurrence.
func setVersionField(text, value string) string {
	loc := versionFieldRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	return text[:loc[4]] + value + text[loc[5]:]
}

// --- small helpers ---

func readPkg(path string) pkgJSON {
	var pj pkgJSON
	if content, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(content, &pj)
	}
	return pj
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// mergeSigningEnv appends the resolved signing secrets to base as KEY=VALUE
// entries (sorted, for determinism), returning base unchanged when there is
// nothing to sign with.
func mergeSigningEnv(base []string, s *plugin.SigningCreds) []string {
	if s == nil || len(s.Env) == 0 {
		return base
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := append([]string{}, base...)
	for _, k := range keys {
		out = append(out, k+"="+s.Env[k])
	}
	return out
}

// signedSuffix is a short " (signed)" tag for build messages.
func signedSuffix(signed bool) string {
	if signed {
		return " (signed)"
	}
	return ""
}

// runCmdEnv runs name+args in dir with the given environment (nil = inherit the
// parent's) and returns captured stdout/stderr; a non-zero exit wraps stderr.
func runCmdEnv(ctx context.Context, dir string, env []string, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()
	if err != nil {
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	return stdout, stderr, err
}
