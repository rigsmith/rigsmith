// Package tauri implements the Tauri desktop-app ecosystem adapter. A Tauri app
// is a Rust crate plus a tauri.conf.json; it is not published to a registry but
// distributed as native installers (.dmg/.msi/NSIS/.AppImage/.deb/…) attached to
// a forge release. So this adapter overlays the cargo adapter: it claims the
// crate sitting next to a tauri.conf.json (see EcosystemInfo.Overlays and the
// discovery reconciliation in commands.Workspace.Discover) and owns that unit's
// version + build, while cargo continues to own ordinary library crates.
//
// Version source of truth (Tauri's own rule, mirrored here):
//   - tauri.conf.json "version" is a semver  → conf-sourced: that file holds the
//     version, and Cargo.toml is kept in lockstep on a bump.
//   - "version" is empty / a path / absent   → cargo-sourced: the sibling
//     Cargo.toml [package] version is the single source.
//
// Like the cargo adapter, files are hand-edited with targeted, format-preserving
// replacements rather than re-serialized, so comments and layout survive.
package tauri

import (
	"bufio"
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

// Adapter is the in-process Tauri ecosystem adapter.
type Adapter struct{}

// New returns a Tauri adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// confName is the Tauri config file this adapter recognizes. Tauri also supports
// Tauri.toml / tauri.conf.json5 variants; v1 handles the canonical JSON file and
// leaves the others to a follow-up (they parse differently).
const confName = "tauri.conf.json"

// Info returns the Tauri adapter's identity and capabilities. It overlays cargo:
// a crate under a tauri.conf.json is released as a desktop app, not a crate.
// Publish is intentionally absent — a Tauri app ships via git tag + forge release,
// so there is no registry push (Artifacts builds the installers to attach).
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "tauri",
		DisplayName:      "Tauri",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{confName},
		Overlays:         []string{"cargo"},
		DevCommands: map[string][]string{
			plugin.VerbBuild: {"cargo", "tauri", "build"},
			plugin.VerbRun:   {"cargo", "tauri", "dev"},
		},
	}
}

// Detect reports whether a tauri.conf.json exists anywhere under root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// Discover walks SourcePath for tauri.conf.json files and returns one Package per
// app. The package's Dir is the crate directory (the directory of the sibling
// Cargo.toml) so it matches the cargo package the overlay reconciliation drops;
// its Name is the crate name, so existing changesets keep resolving after the
// hand-off from cargo.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	var resp plugin.DiscoverResponse
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) != confName {
			return nil
		}
		pkg, ok := a.packageAt(root, path)
		if ok {
			resp.Packages = append(resp.Packages, pkg)
		}
		return nil
	})
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}
	return resp, nil
}

// packageAt builds the Package for a tauri.conf.json at confPath. The sibling
// Cargo.toml supplies the crate name (and, in cargo-sourced mode, the version),
// keeping Dir/Name aligned with the cargo adapter. A conf without a sibling
// Cargo.toml (not a real Tauri app) is skipped.
func (a *Adapter) packageAt(root, confPath string) (plugin.Package, bool) {
	dir := filepath.Dir(confPath)
	cargoPath := filepath.Join(dir, "Cargo.toml")
	crateName, crateVersion, ok := readCrate(cargoPath)
	if !ok {
		return plugin.Package{}, false
	}

	conf := readConf(confPath)
	version := crateVersion
	versionFile := relTo(root, cargoPath)
	if v := strings.TrimSpace(conf.version()); isSemver(v) {
		// Conf-sourced: tauri.conf.json holds the authoritative version.
		version = v
		versionFile = relTo(root, confPath)
	}

	name := crateName
	if name == "" {
		if pn := conf.productName(); pn != "" {
			name = pn
		} else {
			name = filepath.Base(dir)
		}
	}

	return plugin.Package{
		Name:         name,
		Version:      version,
		Dir:          relTo(root, dir),
		ManifestPath: relTo(root, confPath),
		VersionFile:  versionFile,
		// A Tauri app's Cargo.toml is usually publish=false, but that does not make
		// it "private" in shiprig's sense — it is released as a forge artifact, not
		// withheld. Leave Private=false so Artifacts/forge-release run.
	}, true
}

// SetVersion stamps the new version into the app's version source, honoring the
// lockstep rule: in conf-sourced mode it writes both tauri.conf.json and the
// sibling Cargo.toml; in cargo-sourced mode it writes only Cargo.toml. Any
// intra-repo dependency-range updates are applied to the Cargo.toml.
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	confPath := filepath.Join(req.RepoRoot, req.Package.ManifestPath)
	dir := filepath.Dir(confPath)
	cargoPath := filepath.Join(dir, "Cargo.toml")

	conf := readConf(confPath)
	confSourced := isSemver(strings.TrimSpace(conf.version()))

	// Stamp tauri.conf.json's own "version" only when it carries one.
	if confSourced {
		if err := rewriteConfVersion(confPath, req.NewVersion); err != nil {
			return err
		}
	}

	// Stamp the crate (lockstep in conf-sourced mode; the sole source otherwise)
	// and apply any dependency-range rewrites there.
	if _, err := os.Stat(cargoPath); err == nil {
		content, err := os.ReadFile(cargoPath)
		if err != nil {
			return err
		}
		text := setPackageVersion(string(content), req.NewVersion)
		for _, du := range req.DependencyUpdates {
			text = setDependencyRange(text, du.Name, du.NewVersion)
		}
		if err := os.WriteFile(cargoPath, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Publish is a no-op: a Tauri app is released by the publish tagging phase and the
// forge release (Artifacts builds the installers to attach), not pushed to a
// registry. (Not advertised in Capabilities.)
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "Tauri app: released via git tag + forge release"}, nil
}

// Artifacts runs `cargo tauri build` and collects the produced installers so the
// forge-release step can attach them. Tauri writes to its own cargo target dir
// (shared at the workspace root in a workspace), so we locate the bundle dir
// rather than build into req.OutputDir.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	crateDir := filepath.Join(req.RepoRoot, req.Package.Dir)
	signed := req.Signing != nil && len(req.Signing.Env) > 0
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would cargo tauri build %s@%s%s", req.Package.Name, req.Package.Version, signedSuffix(signed))}, nil
	}

	// Signing secrets (when enabled) ride in via the environment — `cargo tauri
	// build` and its bundlers read the standard APPLE_*/TAURI_SIGNING_* variables.
	env := mergeSigningEnv(os.Environ(), req.Signing)
	if _, _, err := runCmdEnv(ctx, crateDir, env, "cargo", "tauri", "build"); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("tauri build: %w", err)
	}

	bundleDir := findBundleDir(crateDir, req.RepoRoot)
	if bundleDir == "" {
		return plugin.ArtifactsResponse{Built: true, Message: "tauri build succeeded but no bundle directory was found"}, nil
	}
	arts := collectBundles(bundleDir)
	return plugin.ArtifactsResponse{
		Built:     true,
		Artifacts: arts,
		Message:   fmt.Sprintf("built %d installer(s) for %s@%s%s", len(arts), req.Package.Name, req.Package.Version, signedSuffix(signed)),
	}, nil
}

// ReleaseInit declares Tauri's release prerequisites. There is no registry token:
// distribution is the forge release, which the release engine already preflights
// (gh/glab/tea). It points at the build prerequisite (the Tauri CLI) and notes
// that signing/notarization secrets are only required when signing is enabled
// (the optional signing seam declares those, not this adapter).
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	return plugin.ReleaseInitResponse{
		Notes: []string{
			"Tauri app: installers are built with `cargo tauri build` and attached to the forge release (no registry push)",
			"requires the Tauri CLI on PATH (cargo-tauri or the npm @tauri-apps/cli)",
			"code-signing/notarization is optional and off by default — enable it via the signing config to sign the installers",
		},
	}, nil
}

// --- bundle collection (testable without a toolchain) ---

// installerExts maps a Tauri bundle file extension to the artifact kind used for
// display and the release-attach decision. Every installer is a release asset
// (Attach: true).
var installerExts = map[string]string{
	".dmg":        plugin.ArtifactBinary,
	".app.tar.gz": plugin.ArtifactArchive,
	".msi":        plugin.ArtifactBinary,
	".exe":        plugin.ArtifactBinary, // NSIS / setup
	".appimage":   plugin.ArtifactBinary,
	".deb":        plugin.ArtifactBinary,
	".rpm":        plugin.ArtifactBinary,
}

// findBundleDir returns the Tauri bundle directory, searching crateDir and its
// ancestors up to repoRoot for a target/release/bundle (cargo shares one target
// dir at the workspace root, so a per-crate build still lands there). Empty when
// none exists.
func findBundleDir(crateDir, repoRoot string) string {
	dir := crateDir
	for {
		candidate := filepath.Join(dir, "target", "release", "bundle")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		if dir == repoRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// collectBundles walks a Tauri bundle directory and returns one Attach:true
// Artifact per installer, plus any updater manifest (latest.json) and detached
// signature (.sig) the build emitted — Decision #5 attaches an updater manifest
// the builder produces but does not generate one.
func collectBundles(bundleDir string) []plugin.Artifact {
	var arts []plugin.Artifact
	_ = filepath.WalkDir(bundleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		switch {
		case name == "latest.json": // updater manifest
			arts = append(arts, plugin.Artifact{Path: path, Attach: true})
		case strings.HasSuffix(name, ".sig"): // updater signature
			arts = append(arts, plugin.Artifact{Path: path, Attach: true})
		default:
			if kind, ok := installerKind(name); ok {
				arts = append(arts, plugin.Artifact{Path: path, Kind: kind, Attach: true})
			}
		}
		return nil
	})
	return arts
}

// installerKind returns the artifact kind for a (lowercased) bundle filename,
// matching the longest known extension first (so .app.tar.gz beats .gz). The
// bool is false for files that aren't installers.
func installerKind(name string) (string, bool) {
	if strings.HasSuffix(name, ".app.tar.gz") {
		return plugin.ArtifactArchive, true
	}
	for ext, kind := range installerExts {
		if ext != ".app.tar.gz" && strings.HasSuffix(name, ext) {
			return kind, true
		}
	}
	return "", false
}

// --- tauri.conf.json reading ---

// tauriConf is the subset of a tauri.conf.json the adapter reads. The version and
// product name are top-level in Tauri v2 and nested under "package" in v1; both
// shapes are accepted.
type tauriConf struct {
	Version     string `json:"version"`
	ProductName string `json:"productName"`
	Package     struct {
		Version     string `json:"version"`
		ProductName string `json:"productName"`
	} `json:"package"`
}

func (c tauriConf) version() string {
	if c.Version != "" {
		return c.Version
	}
	return c.Package.Version
}

func (c tauriConf) productName() string {
	if c.ProductName != "" {
		return c.ProductName
	}
	return c.Package.ProductName
}

// readConf parses a tauri.conf.json; a missing/invalid file yields a zero conf
// (treated as cargo-sourced), which is the safe default.
func readConf(path string) tauriConf {
	var c tauriConf
	content, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(content, &c)
	return c
}

// confVersionRe matches the first `"version": "..."` pair in a JSON document.
var confVersionRe = regexp.MustCompile(`("version"\s*:\s*")([^"]*)(")`)

// rewriteConfVersion replaces the first "version" string in the tauri.conf.json,
// preserving the rest byte-for-byte. The canonical Tauri config places "version"
// (v2) or "package.version" (v1) before any other "version" key, so the first
// match is the app version — mirroring the node adapter's first-match rationale.
func rewriteConfVersion(path, newVersion string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	loc := confVersionRe.FindSubmatchIndex(content)
	if loc == nil {
		return fmt.Errorf("tauri: no \"version\" field in %s", path)
	}
	// Group 2 (the value) spans [loc[4], loc[5]); splice the new value literally.
	updated := string(content[:loc[4]]) + newVersion + string(content[loc[5]:])
	return os.WriteFile(path, []byte(updated), 0o644)
}

// isSemver reports whether v looks like a concrete version (starts with a digit),
// distinguishing a conf-held semver from an empty/path "version" that defers to
// Cargo.toml.
func isSemver(v string) bool {
	return v != "" && v[0] >= '0' && v[0] <= '9'
}

// --- minimal Cargo.toml reading/writing (scoped to what the app needs) ---

var (
	tableRe      = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	keyStringRe  = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*"([^"]*)"\s*$`)
	depVersionRe = regexp.MustCompile(`\bversion\s*=\s*"([^"]*)"`)
)

// readCrate reads the [package] name and version from a Cargo.toml. ok is false
// when the file is missing or has no [package] (a virtual workspace manifest).
func readCrate(path string) (name, version string, ok bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	section := ""
	sc := bufio.NewScanner(strings.NewReader(string(content)))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if h := tableRe.FindStringSubmatch(trimmed); h != nil {
			section = strings.TrimSpace(h[1])
			if section == "package" {
				ok = true
			}
			continue
		}
		if section == "package" {
			if kv := keyStringRe.FindStringSubmatch(line); kv != nil {
				switch kv[1] {
				case "name":
					name = kv[2]
				case "version":
					version = kv[2]
				}
			}
		}
	}
	return name, version, ok
}

// setPackageVersion replaces the `version = "..."` line inside the [package]
// table only, leaving dependency versions untouched.
func setPackageVersion(text, newVersion string) string {
	return rewriteInTable(text, "package", func(line string) (string, bool) {
		if kv := keyStringRe.FindStringSubmatch(line); kv != nil && kv[1] == "version" {
			return replaceFirstQuoted(line, newVersion), true
		}
		return line, false
	})
}

// setDependencyRange rewrites the version range of a named crate wherever it
// appears in a [*dependencies] table (plain-string and inline-table shapes).
func setDependencyRange(text, name, newRange string) string {
	for _, section := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		text = rewriteInTable(text, section, func(line string) (string, bool) {
			eq := strings.IndexByte(line, '=')
			if eq < 0 || strings.TrimSpace(line[:eq]) != name {
				return line, false
			}
			rhs := strings.TrimSpace(line[eq+1:])
			switch {
			case strings.HasPrefix(rhs, `"`):
				return replaceFirstQuoted(line, newRange), true
			case strings.HasPrefix(rhs, "{") && depVersionRe.MatchString(rhs):
				return depVersionRe.ReplaceAllString(line, `version = "`+newRange+`"`), true
			}
			return line, false
		})
	}
	return text
}

// rewriteInTable invokes fn on each line of the named TOML table and substitutes
// any line fn rewrites, preserving layout and line endings.
func rewriteInTable(text, table string, fn func(line string) (string, bool)) string {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	inTable := false
	for i, line := range lines {
		if h := tableRe.FindStringSubmatch(strings.TrimSpace(line)); h != nil {
			inTable = strings.TrimSpace(h[1]) == table
			continue
		}
		if inTable {
			if rewritten, ok := fn(line); ok {
				lines[i] = rewritten
			}
		}
	}
	return strings.Join(lines, newline)
}

// replaceFirstQuoted swaps the contents of the first "double-quoted" run in line.
func replaceFirstQuoted(line, value string) string {
	start := strings.IndexByte(line, '"')
	if start < 0 {
		return line
	}
	end := strings.IndexByte(line[start+1:], '"')
	if end < 0 {
		return line
	}
	end += start + 1
	return line[:start+1] + value + line[end:]
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
// nothing to sign with — so an unsigned build inherits the ambient environment.
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
