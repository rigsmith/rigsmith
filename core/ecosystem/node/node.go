// Package node implements the Node ecosystem adapter: it discovers workspace
// package.json files, reads their name/version/private flag and intra-repo
// dependencies, and rewrites the version (and dependency ranges) format-
// preservingly.
//
// Workspaces are resolved from pnpm-workspace.yaml (packages:) or the root
// package.json "workspaces" field. To keep core dependency-free the pnpm YAML is
// hand-parsed (a simple line scan) rather than pulling in a YAML library.
package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/cmderr"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Adapter is the in-process Node ecosystem adapter.
type Adapter struct{}

// New returns a Node adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// Info returns the Node adapter's identity and capabilities.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "node",
		DisplayName:      "Node",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{"package.json"},
		// Canonical npm commands; rig applies package-manager detection
		// (pnpm/yarn/bun) on top — see cli/internal/detect.
		DevCommands: map[string][]string{
			plugin.VerbBuild:     {"npm", "run", "build"},
			plugin.VerbTest:      {"npm", "test"},
			plugin.VerbRun:       {"npm", "run", "dev"},
			plugin.VerbFormat:    {"npm", "run", "format"},
			plugin.VerbLint:      {"npm", "run", "lint"},
			plugin.VerbTypecheck: {"npm", "run", "typecheck"},
			plugin.VerbCoverage:  {"npm", "run", "coverage"},
			plugin.VerbInstall:   {"npm", "install"},
			plugin.VerbCI:        {"npm", "ci"},
			plugin.VerbAdd:       {"npm", "install"},
			plugin.VerbUninstall: {"npm", "uninstall"},
			plugin.VerbOutdated:  {"npm", "outdated"},
			plugin.VerbUpgrade:   {"npm", "update"},
		},
	}
}

// Detect reports whether a package.json exists at the repo root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	// Recursive: any package.json under root counts, so the ecosystem is detected
	// even when packages live in a subdir (e.g. node/ in a polyglot monorepo).
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// packageJSON is the subset of package.json fields the adapter reads.
type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Private         bool              `json:"private"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	PeerDeps        map[string]string `json:"peerDependencies"`
	OptionalDeps    map[string]string `json:"optionalDependencies"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

// Discover resolves the workspace's package.json files and returns one Package
// each, with intra-repo dependencies (those whose name matches another discovered
// package) attached.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	manifestPaths, err := workspaceManifests(scanRoot)
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}

	// First pass: parse every manifest and collect the set of intra-repo names.
	type parsed struct {
		path string
		pj   packageJSON
	}
	var all []parsed
	names := map[string]bool{}
	for _, p := range manifestPaths {
		content, err := os.ReadFile(p)
		if err != nil {
			return plugin.DiscoverResponse{}, err
		}
		var pj packageJSON
		if err := json.Unmarshal(content, &pj); err != nil {
			return plugin.DiscoverResponse{}, err
		}
		if pj.Name == "" {
			continue
		}
		all = append(all, parsed{path: p, pj: pj})
		names[pj.Name] = true
	}

	// Second pass: build packages, keeping only intra-repo dependency edges.
	var resp plugin.DiscoverResponse
	for _, pr := range all {
		pkg := plugin.Package{
			Name:         pr.pj.Name,
			Version:      pr.pj.Version,
			Dir:          relTo(root, filepath.Dir(pr.path)),
			ManifestPath: relTo(root, pr.path),
			Private:      pr.pj.Private,
			Dependencies: intraRepoDeps(pr.pj, names),
		}
		resp.Packages = append(resp.Packages, pkg)
	}
	return resp, nil
}

// SetVersion rewrites the "version" field and any named dependency ranges in the
// package.json, preserving formatting (a targeted line replace rather than a
// re-marshal, which would reorder keys).
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
	text := string(content)

	text = setStringField(text, "version", req.NewVersion)
	for _, du := range req.DependencyUpdates {
		text = setDependencyRange(text, du.Name, du.NewVersion)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// Publish runs `npm publish` for the package, after a registry pre-check.
//
// Idempotency: `npm view <name>@<version> version` is queried first; when it
// succeeds and echoes back the same version the package is already published, so
// we skip. Access defaults to "restricted" unless req.Access is an explicit
// "public"/"restricted". The registry is npmRegistry's: a URL-shaped req.PackageSource, else the package's
// publishConfig.registry, else the nearest .npmrc's routing up to the repository root.
//
// npmPublish runs `npm publish`: a variable so a test can see its arguments.
var npmPublish = func(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, err error) {
	return runCmdEnv(ctx, dir, env, "npm", args...)
}

// npmView runs `npm view` in dir, with env (nil = inherit): a variable so a test can stand in for npm.
var npmView = func(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, err error) {
	return runCmdEnv(ctx, dir, env, "npm", append([]string{"view"}, args...)...)
}

// Published asks npm whether the version is on the registry, as @changesets'
// publish-plan does: `npm view <name>@<version> version` prints the version
// when it's there and nothing when the package exists without it, and fails
// with E404 when the package doesn't exist at all. Any other failure is an
// error rather than "not published". A private package has no registry.
//
// The lookup goes out with the caller's own npm auth. Only when the registry refuses it (E401/E403, a private feed)
// and a publish credential was supplied is it asked once more with that credential, written for the registry's host
// alone — npm sends a host-keyed token nowhere else.
func (a *Adapter) Published(ctx context.Context, req plugin.PublishedRequest) (plugin.PublishedResponse, error) {
	if req.Package.Private {
		return plugin.PublishedResponse{NoRegistry: true}, nil
	}
	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	spec := req.Package.Name + "@" + req.Package.Version
	registry, err := npmRegistry(req.RepoRoot, req.Package, req.PackageSource)
	if err != nil {
		return plugin.PublishedResponse{}, fmt.Errorf("npm registry for %s: %w", req.Package.Name, err)
	}
	args := append([]string{spec, "version"}, npmRegistryArgs(req.Package.Name, registry)...)
	out, stderr, err := npmView(ctx, dir, nil, args...)
	if err != nil && req.Auth != nil && req.Auth.Token != "" && (strings.Contains(stderr, "E401") || strings.Contains(stderr, "E403")) {
		env, cleanup, aerr := npmAuthConfig(req.Auth.Token, registry)
		if aerr != nil {
			return plugin.PublishedResponse{}, fmt.Errorf("npm view %s: %w", spec, aerr)
		}
		defer cleanup()
		out, stderr, err = npmView(ctx, dir, env, args...)
	}
	if err != nil {
		if strings.Contains(stderr, "E404") {
			return plugin.PublishedResponse{}, nil
		}
		return plugin.PublishedResponse{}, fmt.Errorf("npm view %s: %s", spec, strings.TrimSpace(stderr+" "+err.Error()))
	}
	switch strings.TrimSpace(out) {
	case req.Package.Version:
		return plugin.PublishedResponse{Published: true}, nil
	case "":
		return plugin.PublishedResponse{}, nil
	default:
		return plugin.PublishedResponse{}, fmt.Errorf("npm view %s printed %q, not the version or nothing", spec, strings.TrimSpace(out))
	}
}

// Credentials: npm uses the caller's npm auth (~/.npmrc / NPM_TOKEN), which we do
// not manage here.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	if req.Package.Private {
		return plugin.PublishResponse{Skipped: true, Message: "private"}, nil
	}

	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	spec := req.Package.Name + "@" + req.Package.Version
	// The registry the workspace routes this package to (npmRegistry), for the pre-check, the publish and its auth
	// alike — npm run in the package directory would otherwise miss a workspace-root .npmrc and use npmjs.com.
	registry, err := npmRegistry(req.RepoRoot, req.Package, req.PackageSource)
	if err != nil {
		// never fall back to npm's default: that would send a private package to npmjs.com
		return plugin.PublishResponse{}, fmt.Errorf("npm registry for %s: %w", req.Package.Name, err)
	}
	viewArgs := append([]string{spec, "version"}, npmRegistryArgs(req.Package.Name, registry)...)

	// Pre-check: a clean exit echoing the requested version means it already exists.
	// A non-zero exit (unpublished version / network) is treated as "not present"
	// and we proceed to publish, where npm will surface any real failure.
	if out, _, err := npmView(ctx, dir, nil, viewArgs...); err == nil {
		if strings.TrimSpace(out) == req.Package.Version {
			return plugin.PublishResponse{Skipped: true, Message: "already published"}, nil
		}
	}

	// Checked before the dry run returns, so a preview shows a bad value too.
	provenance, err := provenanceMode(req.Provenance)
	if err != nil {
		return plugin.PublishResponse{}, err
	}

	// npm publishes `workspace:` ranges as written, which no registry can
	// resolve. In a pnpm workspace, pnpm packs the tarball (rewriting them to
	// the real versions) and npm publishes that file, so auth, OIDC and
	// provenance stay npm's. Elsewhere a `workspace:` range is refused rather
	// than shipped.
	// The refusal holds for a packed artifact too: outside pnpm, `shiprig
	// pack` ran npm pack, which leaves the ranges as written.
	inPnpm := pnpmWorkspace(req.RepoRoot, dir)
	pnpm := req.ArtifactPath == "" && inPnpm
	if !inPnpm {
		if err := refuseWorkspaceProtocol("npm publish "+spec, req.Package); err != nil {
			return plugin.PublishResponse{}, err
		}
	}

	if req.DryRun {
		verb := "npm publish"
		if pnpm {
			verb = "pnpm pack and npm publish"
		}
		return plugin.PublishResponse{
			Published: false,
			Message:   fmt.Sprintf("dry-run: would %s %s", verb, spec),
		}, nil
	}

	access := req.Access
	if access != "public" && access != "restricted" {
		access = "restricted"
	}
	args := []string{"publish"}
	// A file `shiprig pack` built goes out as it is; npm publish takes a
	// tarball path in place of the package directory.
	artifact := req.ArtifactPath
	packNote := ""
	// npm publishes a tarball without running any of the package's publish
	// scripts, and pnpm pack runs only prepack, prepare and postpack. The
	// rest of the sequence a directory publish runs — prepublishOnly before
	// the pack, publish and postpublish after the upload — is run here, in
	// npm's order, unless scripts are turned off. A file `shiprig pack` built
	// in a pnpm workspace had prepublishOnly run before its pack (Artifacts),
	// so only publish and postpublish are left for the upload.
	var scripts map[string]bool
	if inPnpm && !scriptsIgnored(ctx, dir) {
		scripts = packageScripts(dir)
	}
	if pnpm && scripts["prepublishOnly"] {
		if err := runPackageScript(ctx, dir, "prepublishOnly"); err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: prepublishOnly: %w", spec, err)
		}
	}
	if pnpm {
		tmp, err := os.MkdirTemp("", "shiprig-pnpm-pack-")
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("pnpm pack: temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		if artifact, err = pnpmPackTarball(ctx, dir, tmp); err != nil {
			return plugin.PublishResponse{}, err
		}
		packNote = " (packed with pnpm)"
	}
	if artifact != "" {
		args = append(args, artifact)
	}
	args = append(args, "--access", access)
	if req.Tag != "" {
		args = append(args, "--tag", req.Tag)
	}
	args = append(args, npmRegistryArgs(req.Package.Name, registry)...)

	// Resolve the publish credential. With nothing supplied, npm uses the
	// caller's ambient auth (~/.npmrc / NPM_TOKEN) and we touch nothing. With a
	// token (an engine-resolved secret ref, or one we mint via OIDC), write it
	// to a temp npmrc seeded from the existing one (so scopes/registries
	// survive) and point npm at it via NPM_CONFIG_USERCONFIG — env config layers
	// above project/user .npmrc without putting the token on the command line.
	env := os.Environ()
	authNote := ""
	provenanceNote := ""
	// The CI id-token is minted for npmjs.com's trusted publishing and is exchanged where it always was: a configured
	// package source, else npmjs.com. A registry found in publishConfig or an .npmrc is never sent it — OIDC is skipped
	// there, as `oidc: off` would, and npm publishes with its own auth for that registry.
	oidc := req.OIDC
	if oidc && registry != "" && !strings.EqualFold(npmRegistryBase(registry), npmRegistryBase(req.PackageSource)) {
		oidc = false
		authNote = " (OIDC skipped: trusted publishing exchanges at " + npmRegistryBase(req.PackageSource) + ", not this registry; npm's own auth used)"
	}
	switch {
	case req.Auth != nil && req.Auth.Token != "":
		authEnv, cleanup, err := npmAuthConfig(req.Auth.Token, registry)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		defer cleanup()
		env = authEnv
		if req.Auth.Method == "secret-ref" {
			authNote = " (auth via secret reference)"
		}

	case oidc:
		token, err := oidcPublishToken(ctx, req.Package.Name, req.PackageSource)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		authEnv, cleanup, err := npmAuthConfig(token, registry)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		defer cleanup()
		env = authEnv
		authNote = " (auth via OIDC trusted publishing)"
	}

	// Provenance is decided apart from auth: a token publish (every first
	// publish of a new package) attests as well as an OIDC one. Auth never
	// depends on the npm version, only the attestation does, so `auto` on a
	// runner whose npm is too old still publishes.
	minNpm := provenanceMinNpmToken
	if oidc && (req.Auth == nil || req.Auth.Token == "") {
		minNpm = provenanceMinNpmOIDC
	}
	// npm attests a package published with access other than public only
	// when the registry already holds it as public; it refuses a new or
	// private one ("Can't generate provenance for new or private package").
	// The registry is asked only when an attestation is otherwise on, and a
	// lookup that failed is reported as that, not as a new or private package.
	if access != "public" && provenance != provenanceOff && provenanceIDTokenAvailable() {
		public, lerr := npmPackagePublic(ctx, dir, env, req.Package.Name, npmRegistryArgs(req.Package.Name, registry))
		switch {
		case lerr != nil && provenance == provenanceAlways:
			return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: node.provenance is \"always\", but npm attests a package published with access %s only when the registry already holds it as public, "+
				"and the registry visibility lookup failed: %v", spec, access, lerr)
		case lerr != nil:
			provenanceNote = fmt.Sprintf(" (provenance skipped — registry visibility lookup failed: %v)", lerr)
			provenance = provenanceOff
		case !public && provenance == provenanceAlways:
			return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: node.provenance is \"always\", but npm attests a package published with access %s only when the registry already holds it as public, "+
				"and this one is new or private; set access to public, or provenance to auto or off", spec, access)
		case !public:
			provenanceNote = " (provenance skipped — npm attests only public packages)"
			provenance = provenanceOff
		}
	}
	// The registry also refuses a statement whose source is a private
	// GitHub repository (HTTP 422, "Unsupported GitHub Actions source
	// repository visibility"), which npm doesn't check before uploading.
	if provenance != provenanceOff && provenanceIDTokenAvailable() {
		if vis := githubRepositoryVisibility(); vis != "" && vis != "public" {
			switch provenance {
			case provenanceAlways:
				return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: node.provenance is \"always\", but this workflow's repository is %s, and npm accepts provenance only from a public repository; "+
					"set provenance to auto or off", spec, vis)
			case provenanceAuto:
				provenanceNote = " (provenance skipped — npm accepts it only from a public repository, and this one is " + vis + ")"
				provenance = provenanceOff
			}
		}
	}
	attest, note, err := decideProvenance(provenance, provenanceIDTokenAvailable(), minNpm, func() bool {
		return npmSupportsProvenance(ctx, minNpm)
	})
	if err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: %w", spec, err)
	}
	// node.provenance decides, not npm's own config: leaving the flag out
	// when not attesting would let an .npmrc's provenance=true (or
	// NPM_CONFIG_PROVENANCE) have npm attest anyway, which fails a publish
	// with no id-token to sign with, or a restricted one. The flag outranks
	// both, either way.
	if attest {
		args = append(args, "--provenance")
	} else {
		args = append(args, "--provenance=false")
	}
	if note != "" {
		provenanceNote = note
	}

	if _, _, err := npmPublish(ctx, dir, env, args...); err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
	}
	for _, name := range []string{"publish", "postpublish"} {
		if scripts[name] {
			if err := runPackageScript(ctx, dir, name); err != nil {
				return plugin.PublishResponse{}, fmt.Errorf("npm publish %s: published, but its %s script failed: %w", spec, name, err)
			}
		}
	}

	return plugin.PublishResponse{Published: true, Message: "published " + spec + authNote + provenanceNote + packNote}, nil
}

// Provenance modes, the ecosystem block's `provenance` values.
const (
	provenanceAuto   = "auto"
	provenanceAlways = "always"
	provenanceOff    = "off"
)

// The npm a provenance attestation needs: --provenance arrived in npm 9.5.0;
// shiprig's OIDC path has asked for 11.5.1, the npm that brought trusted
// publishing, since it was written, and still does.
var (
	provenanceMinNpmToken = [3]int{9, 5, 0}
	provenanceMinNpmOIDC  = [3]int{11, 5, 1}
)

// provenanceMode normalizes the `provenance` setting: empty is auto, and
// anything but auto/always/off is an error rather than a silent default.
func provenanceMode(v string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case "":
		return provenanceAuto, nil
	case provenanceAuto, provenanceAlways, provenanceOff:
		return m, nil
	}
	return "", fmt.Errorf("node.provenance is %q: want \"auto\", \"always\" or \"off\"", v)
}

// githubRepositoryVisibility is the GitHub Actions workflow's repository
// visibility — "public", "private" or "internal" — read from the event
// payload GitHub writes to GITHUB_EVENT_PATH, or "" when it can't be told
// (not in Actions, or a payload without the repository). An older payload
// with only `private` counts as private or public by it.
func githubRepositoryVisibility() string {
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var event struct {
		Repository *struct {
			Visibility string `json:"visibility"`
			Private    *bool  `json:"private"`
		} `json:"repository"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Repository == nil {
		return ""
	}
	switch {
	case event.Repository.Visibility != "":
		return strings.ToLower(event.Repository.Visibility)
	case event.Repository.Private != nil && *event.Repository.Private:
		return "private"
	case event.Repository.Private != nil:
		return "public"
	}
	return ""
}

// provenanceIDTokenAvailable reports whether npm can get the id-token it signs
// a provenance statement with: GitHub Actions with `id-token: write`, which
// is what sets ACTIONS_ID_TOKEN_REQUEST_URL.
func provenanceIDTokenAvailable() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != ""
}

// decideProvenance decides whether the publish passes --provenance, and the
// note the result carries. auto attests when an id-token is available and the
// npm is new enough, saying so when only the npm is missing; always attests
// or fails; off never attests. npmOK is only asked when it matters, since it
// runs npm.
func decideProvenance(mode string, idToken bool, minNpm [3]int, npmOK func() bool) (attest bool, note string, err error) {
	need := fmt.Sprintf("npm ≥ %d.%d.%d", minNpm[0], minNpm[1], minNpm[2])
	switch mode {
	case provenanceOff:
		return false, "", nil
	case provenanceAlways:
		if !idToken {
			return false, "", errors.New("node.provenance is \"always\", but there is no id-token to sign it with: " +
				"provenance needs GitHub Actions with the `id-token: write` permission (ACTIONS_ID_TOKEN_REQUEST_URL is unset)")
		}
		if !npmOK() {
			return false, "", fmt.Errorf("node.provenance is \"always\", but provenance needs %s on the runner", need)
		}
		return true, " (provenance attested)", nil
	default: // auto
		if !idToken {
			return false, "", nil
		}
		if !npmOK() {
			return false, " (provenance skipped — needs " + need + ")", nil
		}
		return true, " (provenance attested)", nil
	}
}

// pnpmWorkspace reports whether the package at dir belongs to a pnpm
// workspace: a pnpm-workspace.yaml or pnpm-lock.yaml in dir or a directory
// above it, up to the repository root.
func pnpmWorkspace(repoRoot, dir string) bool {
	root := filepath.Clean(repoRoot)
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, "pnpm-workspace.yaml")) || fileExists(filepath.Join(d, "pnpm-lock.yaml")) {
			return true
		}
		if d == root || filepath.Dir(d) == d || !strings.HasPrefix(d, root) {
			return false
		}
	}
}

// refuseWorkspaceProtocol is the error for a package outside a pnpm workspace
// that names an in-repo dependency with the workspace: protocol, or nil.
func refuseWorkspaceProtocol(what string, p plugin.Package) error {
	dep, ok := workspaceProtocolDep(p)
	if !ok {
		return nil
	}
	return fmt.Errorf("%s: %s is %q, and npm writes workspace: ranges into the package unrewritten; "+
		"only a pnpm workspace (pnpm-workspace.yaml or pnpm-lock.yaml) is packed with pnpm, which rewrites them", what, dep.Name, dep.Range)
}

// workspaceProtocolDep returns an in-repo dependency written with pnpm's
// workspace: protocol, which only pnpm's pack rewrites. Discovery records
// dependencies, peer and optional dependencies; a devDependency isn't
// installed for the package's users, so it doesn't count.
func workspaceProtocolDep(p plugin.Package) (plugin.Dependency, bool) {
	for _, d := range p.Dependencies {
		if d.Kind != plugin.DepDev && strings.HasPrefix(d.Range, "workspace:") {
			return d, true
		}
	}
	return plugin.Dependency{}, false
}

// npmAccessStatus runs `npm access get status` in dir, with env: a variable
// so a test can stand in for npm.
var npmAccessStatus = func(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, err error) {
	return runCmdEnv(ctx, dir, env, "npm", append([]string{"access", "get", "status", "--json"}, args...)...)
}

// npmPackagePublic reports whether the registry holds the package as public,
// as npm itself checks before it attests one published with access other
// than public. A package the registry doesn't have (E404: a first publish, or
// a private one the caller can't see) is not public. Any other failure is an
// error rather than "not public", after one more try: a lookup that never
// answered says nothing about the package.
func npmPackagePublic(ctx context.Context, dir string, env []string, name string, registryArgs []string) (bool, error) {
	args := append([]string{name}, registryArgs...)
	out, stderr, err := npmAccessStatus(ctx, dir, env, args...)
	if err != nil && !npmErrorCode.MatchString(stderr) {
		out, stderr, err = npmAccessStatus(ctx, dir, env, args...)
	}
	if err != nil {
		if npmErrorCode.MatchString(stderr) {
			return false, nil
		}
		return false, errors.New(redactCredentials(strings.TrimSpace(stderr + " " + err.Error())))
	}
	var status map[string]string
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return false, fmt.Errorf("npm access get status printed %q: %w", strings.TrimSpace(out), err)
	}
	return status[name] == "public", nil
}

var (
	// npmErrorCode matches the line npm reports a 404's error code on
	// ("npm error code E404", or "npm ERR! code E404" before npm 10), not an
	// E404 anywhere in the output: a request URL npm echoes can hold one.
	npmErrorCode = regexp.MustCompile(`(?m)^npm (?:error|ERR!) code E404\s*$`)
	// urlUserinfo matches the user[:password]@ part of a URL, up to the last
	// '@' before the path: a password can hold an unencoded '@'.
	urlUserinfo = regexp.MustCompile(`(://)[^/?#\s]*@`)
	// authHeaderValue matches a Basic or Bearer credential as npm's verbose
	// output can print it.
	authHeaderValue = regexp.MustCompile(`(?i)\b(basic|bearer)\s+[A-Za-z0-9+/=._~-]{8,}`)
)

// redactCredentials masks credentials npm can echo in its output (a registry
// URL's user:token@, an Authorization value) before that output goes into a
// message: a publish's own message is printed as is, where its errors are
// redacted again by the caller.
func redactCredentials(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "${1}***@")
	return authHeaderValue.ReplaceAllString(s, "$1 ***")
}

// scriptsIgnored reports whether the package's scripts are turned off: an
// ignore-scripts set in the environment or in pnpm's config (.npmrc). A
// variable so a test can stand in for pnpm.
var scriptsIgnored = func(ctx context.Context, dir string) bool {
	for _, k := range []string{"npm_config_ignore_scripts", "NPM_CONFIG_IGNORE_SCRIPTS"} {
		if strings.EqualFold(os.Getenv(k), "true") {
			return true
		}
	}
	out, _, err := runCmd(ctx, dir, "pnpm", "config", "get", "ignore-scripts")
	return err == nil && strings.TrimSpace(out) == "true"
}

// runPackageScript runs one of the package's scripts with pnpm, that script
// alone: pnpm's own pre/post expansion is off, or `pnpm run publish` would
// also run postpublish (run again after it) and prepublish (which npm
// doesn't run on publish). A variable so a test can stand in for pnpm.
var runPackageScript = func(ctx context.Context, dir, name string) error {
	_, _, err := runCmd(ctx, dir, "pnpm", "--config.enable-pre-post-scripts=false", "run", name)
	return err
}

// packageScripts names the scripts the package.json in dir defines.
func packageScripts(dir string) map[string]bool {
	content, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pj struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(content, &pj) != nil {
		return nil
	}
	out := make(map[string]bool, len(pj.Scripts))
	for name := range pj.Scripts {
		out[name] = true
	}
	return out
}

// pnpmPack runs `pnpm pack` in dir, writing the tarball into dest: a variable
// so a test can stand in for pnpm.
var pnpmPack = func(ctx context.Context, dir, dest string) (stdout, stderr string, err error) {
	return runCmd(ctx, dir, "pnpm", "pack", "--pack-destination", dest)
}

// pnpmPackTarball packs the package at dir with pnpm, which rewrites
// workspace: ranges to the versions they resolve to, and returns the
// tarball's path in dest. pnpm packs into an empty directory of its own and
// the one .tgz there is taken, since `pnpm pack --json` has changed shape
// between pnpm versions.
func pnpmPackTarball(ctx context.Context, dir, dest string) (string, error) {
	tmp, err := os.MkdirTemp(dest, ".pnpm-pack-")
	if err != nil {
		return "", fmt.Errorf("pnpm pack: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if _, _, err := pnpmPack(ctx, dir, tmp); err != nil {
		return "", fmt.Errorf("pnpm pack: %w", err)
	}
	found, _ := filepath.Glob(filepath.Join(tmp, "*.tgz"))
	if len(found) != 1 {
		return "", fmt.Errorf("pnpm pack in %s: want one tarball, found %d", dir, len(found))
	}
	out := filepath.Join(dest, filepath.Base(found[0]))
	if err := os.Rename(found[0], out); err != nil {
		return "", fmt.Errorf("pnpm pack: %w", err)
	}
	return out, nil
}

// npmAuthConfig writes a temporary npmrc carrying the auth token for the target
// registry and returns the environment (NPM_CONFIG_USERCONFIG pointed at it)
// plus a cleanup func. The temp file is seeded from the caller's existing user
// npmrc so their scope/registry settings are preserved, then the token line is
// appended (last key wins) — so we add auth without discarding configuration.
func npmAuthConfig(token, packageSource string) (env []string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "shiprig-npmrc-")
	if err != nil {
		return nil, nil, fmt.Errorf("npm auth: temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	var b strings.Builder
	if existing := existingNpmrc(); existing != "" {
		if data, rerr := os.ReadFile(existing); rerr == nil && len(data) > 0 {
			b.Write(data)
			if data[len(data)-1] != '\n' {
				b.WriteByte('\n')
			}
		}
	}
	b.WriteString(npmrcAuthKey(packageSource))
	b.WriteString(token)
	b.WriteByte('\n')

	npmrc := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(npmrc, []byte(b.String()), 0o600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("npm auth: write npmrc: %w", err)
	}
	return append(os.Environ(), "NPM_CONFIG_USERCONFIG="+npmrc), cleanup, nil
}

// existingNpmrc returns the path npm would read as the user config, so we can
// seed our temp file from it: an explicit NPM_CONFIG_USERCONFIG wins, else
// ~/.npmrc. Empty when neither is determinable.
func existingNpmrc() string {
	if p := os.Getenv("NPM_CONFIG_USERCONFIG"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".npmrc")
	}
	return ""
}

// npmrcAuthKey returns the registry-scoped _authToken key prefix for an npmrc,
// e.g. "//registry.npmjs.org/:_authToken=". A URL packageSource is keyed by its
// host+path; anything else (feed name / empty) targets the default registry.
func npmrcAuthKey(packageSource string) string {
	host, path := "registry.npmjs.org", "/"
	if strings.HasPrefix(packageSource, "http") {
		if u, err := url.Parse(packageSource); err == nil && u.Host != "" {
			host = u.Host
			if path = u.Path; path == "" {
				path = "/"
			}
			if !strings.HasSuffix(path, "/") {
				path += "/"
			}
		}
	}
	return "//" + host + path + ":_authToken="
}

// Artifacts builds the npm package tarball (`npm pack`) into req.OutputDir. The
// .tgz is a registry artifact, so it is not attached to the GitHub release by
// default (Attach: false) — it ships to npm via Publish.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	if err := req.CheckProperties(a.Info()); err != nil {
		return plugin.ArtifactsResponse{}, err
	}
	if req.Package.Private {
		return plugin.ArtifactsResponse{Skipped: true, Message: "private"}, nil
	}
	spec := req.Package.Name + "@" + req.Package.Version
	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	// In a pnpm workspace pnpm packs, so the tarball's workspace: ranges are
	// the real versions: the same file Publish would send.
	pnpm := pnpmWorkspace(req.RepoRoot, dir)
	// npm pack writes workspace: ranges into the tarball as they are, and the
	// file is what a later --from-pack-dir publish sends: refused before
	// anything is built, and before the dry run answers, so the preview says
	// what the pack would.
	if !pnpm {
		if err := refuseWorkspaceProtocol("npm pack "+spec, req.Package); err != nil {
			return plugin.ArtifactsResponse{}, err
		}
	}
	if req.DryRun {
		tool := "npm"
		if pnpm {
			tool = "pnpm"
		}
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would %s pack %s", tool, spec)}, nil
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("npm pack: mkdir %s: %w", req.OutputDir, err)
	}
	// prepublishOnly runs before the pack, as npm runs it before its own:
	// the file built here is what a --from-pack-dir publish sends, too late
	// for it then. pnpm pack runs prepack, prepare and postpack itself.
	if pnpm && !scriptsIgnored(ctx, dir) && packageScripts(dir)["prepublishOnly"] {
		if err := runPackageScript(ctx, dir, "prepublishOnly"); err != nil {
			return plugin.ArtifactsResponse{}, fmt.Errorf("pnpm pack %s: prepublishOnly: %w", spec, err)
		}
	}
	if pnpm {
		path, err := pnpmPackTarball(ctx, dir, req.OutputDir)
		if err != nil {
			return plugin.ArtifactsResponse{}, err
		}
		return plugin.ArtifactsResponse{
			Built:     true,
			Artifacts: []plugin.Artifact{{Path: path, Kind: plugin.ArtifactPackage, Attach: false}},
			Message:   "packed " + spec + " (with pnpm)",
		}, nil
	}
	// --json prints the produced tarball's filename on stdout; --pack-destination
	// places it in OutputDir rather than the package directory.
	out, _, err := runCmd(ctx, dir, "npm", "pack", "--pack-destination", req.OutputDir, "--json")
	if err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("npm pack: %w", err)
	}
	var packed []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal([]byte(out), &packed); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("npm pack: parse --json output: %w", err)
	}
	arts := make([]plugin.Artifact, 0, len(packed))
	for _, p := range packed {
		arts = append(arts, plugin.Artifact{
			Path:   filepath.Join(req.OutputDir, p.Filename),
			Kind:   plugin.ArtifactPackage,
			Attach: false,
		})
	}
	return plugin.ArtifactsResponse{Built: true, Artifacts: arts, Message: "packed " + spec}, nil
}

// ReleaseInit declares Node's release prerequisites. With OIDC trusted
// publishing in play (the default), no NPM_TOKEN is required — instead we point
// the operator at the one-time Trusted Publisher setup, which shiprig cannot do
// for them. With OIDC off, it falls back to declaring NPM_TOKEN. npm produces
// its tarball natively (npm pack), so there is no build-config file to scaffold.
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	if req.OIDC {
		return plugin.ReleaseInitResponse{
			Notes: []string{
				"publishes to npm via OIDC trusted publishing — no NPM_TOKEN needed",
				"one-time: add your release workflow as a Trusted Publisher (npmjs.com → package → Settings → Trusted Publisher)",
				"CI must grant `id-token: write` and run actions/setup-node with registry-url; provenance (node.provenance, default auto) needs npm ≥ 11.5.1 here",
				"to use a token instead, set npm.oidc=\"off\" and provide NPM_TOKEN (or npm.auth)",
			},
		}, nil
	}
	return plugin.ReleaseInitResponse{
		Tokens: []plugin.TokenSpec{{
			EnvVar: "NPM_TOKEN",
			For:    "npm publish",
			URL:    "https://www.npmjs.com/settings/~/tokens",
		}},
		Notes: []string{
			"publishes to the npm registry (set npm.oidc to enable tokenless OIDC publishing)",
			"provenance is attested in GitHub Actions with `id-token: write` (node.provenance: auto|always|off; npm ≥ 9.5.0)",
		},
	}, nil
}

// runCmd runs name+args in dir ("" for the current directory) with the ambient
// environment and returns the captured stdout/stderr.
func runCmd(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error) {
	return runCmdEnv(ctx, dir, nil, name, args...)
}

// runCmdEnv is runCmd with an explicit environment (nil = inherit the parent's).
// On a non-zero exit the error wraps stderr for diagnostics.
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
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, cmderr.Detail(stdout, stderr))
	}
	return stdout, stderr, err
}

// intraRepoDeps returns the dependency edges that point at another discovered
// package, tagging each with its kind and the range string as written.
func intraRepoDeps(pj packageJSON, known map[string]bool) []plugin.Dependency {
	var deps []plugin.Dependency
	collect := func(m map[string]string, kind plugin.DependencyKind) {
		// Sort for deterministic output (map iteration is randomized).
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			if known[name] {
				deps = append(deps, plugin.Dependency{Name: name, Kind: kind, Range: m[name]})
			}
		}
	}
	collect(pj.Dependencies, plugin.DepNormal)
	collect(pj.DevDependencies, plugin.DepDev)
	collect(pj.PeerDeps, plugin.DepPeer)
	// Installed with the package like a dependency (unless they fail to
	// build), so a release of one moves the dependent the same way.
	collect(pj.OptionalDeps, plugin.DepNormal)
	return deps
}

// workspaceManifests resolves the package.json files in the workspace. It prefers
// pnpm-workspace.yaml globs, then the root package.json "workspaces" field, and
// finally falls back to walking the tree for every package.json.
func workspaceManifests(root string) ([]string, error) {
	globs := pnpmWorkspaceGlobs(root)
	if globs == nil {
		globs = packageJSONWorkspaces(root)
	}

	if len(globs) > 0 {
		// Only glob-matched directories are workspace packages; the root manifest
		// is the workspace container, not a package (matching npm/yarn/@manypkg).
		return resolveGlobs(root, globs), nil
	}

	// Fallback: walk for all package.json files.
	var out []string
	err := walkutil.Walk(root, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) == "package.json" {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// pnpmWorkspaceGlobs hand-parses the `packages:` list from pnpm-workspace.yaml.
// It scans for `- '<glob>'` (or `- "<glob>"`, or bare) list items under the
// packages: key — enough for the common case without a YAML dependency.
func pnpmWorkspaceGlobs(root string) []string {
	f, err := os.Open(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var globs []string
	inPackages := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A top-level key (no leading whitespace) ends the packages: block.
		if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") && strings.HasSuffix(trimmed, ":") {
			inPackages = trimmed == "packages:"
			continue
		}
		if inPackages && strings.HasPrefix(trimmed, "-") {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			globs = append(globs, unquote(item))
		}
	}
	return globs
}

// packageJSONWorkspaces reads the "workspaces" field from the root package.json.
// It accepts both the array form and the { "packages": [...] } object form.
func packageJSONWorkspaces(root string) []string {
	content, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pj packageJSON
	if err := json.Unmarshal(content, &pj); err != nil || len(pj.Workspaces) == 0 {
		return nil
	}
	var arr []string
	if json.Unmarshal(pj.Workspaces, &arr) == nil {
		return arr
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(pj.Workspaces, &obj) == nil {
		return obj.Packages
	}
	return nil
}

// resolveGlobs expands workspace globs (e.g. "packages/*") into the package.json
// paths under each matched directory. A leading '!' negates the pattern: its
// matches are removed from the included set (npm/yarn exclusion semantics).
func resolveGlobs(root string, globs []string) []string {
	included := map[string]bool{}
	excluded := map[string]bool{}
	for _, g := range globs {
		target := included
		if strings.HasPrefix(g, "!") {
			g = strings.TrimPrefix(g, "!")
			target = excluded
		}
		for _, dir := range expandGlob(root, g) {
			target[dir] = true
		}
	}

	var out []string
	for dir := range included {
		if !excluded[dir] {
			out = append(out, filepath.Join(dir, "package.json"))
		}
	}
	sortStrings(out) // map iteration is randomized; keep discovery deterministic
	return out
}

// expandGlob expands one workspace glob ('/'-separated, relative to root) into
// the existing directories it matches that contain a package.json. '*' matches
// within a single path segment; a bare "**" segment matches any depth, including
// zero. Wildcards never descend into the default skip set (node_modules, .git,
// build output — see walkutil), so dependency trees are never workspaces.
func expandGlob(root, glob string) []string {
	current := []string{root}
	for _, seg := range strings.Split(filepath.ToSlash(glob), "/") {
		if seg == "" {
			continue
		}
		var next []string
		for _, dir := range current {
			switch {
			case seg == "**":
				next = append(next, dir)
				next = append(next, descendantDirs(dir)...)
			case strings.ContainsAny(seg, "*?["):
				for _, sub := range childDirs(dir) {
					if ok, _ := path.Match(seg, filepath.Base(sub)); ok {
						next = append(next, sub)
					}
				}
			default:
				// A literal segment never reaches childDirs, so it needs the
				// same prune: `packages/clone` names it outright.
				candidate := filepath.Join(dir, seg)
				if fi, err := os.Stat(candidate); err == nil && fi.IsDir() && !(!walkutil.IncludeWorktrees() && walkutil.LinkedWorktreeRoot(candidate)) {
					next = append(next, candidate)
				}
			}
		}
		current = next
	}

	var out []string
	for _, dir := range current {
		if fileExists(filepath.Join(dir, "package.json")) {
			out = append(out, dir)
		}
	}
	return out
}

// childDirs lists dir's immediate subdirectories, omitting the default skip set.
// An unreadable (or missing) directory yields none rather than an error.
func childDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || walkutil.SkippedDir(e.Name()) {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		// A workspace glob must prune what Walk prunes, or `packages/*` reaches
		// into a linked worktree and returns a second copy of this repo's own
		// manifests — the thing walkutil stops.
		if !walkutil.IncludeWorktrees() && walkutil.LinkedWorktreeRoot(sub) {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// descendantDirs lists every directory below dir at any depth, pruning the
// default skip set.
func descendantDirs(dir string) []string {
	var out []string
	for _, sub := range childDirs(dir) {
		out = append(out, sub)
		out = append(out, descendantDirs(sub)...)
	}
	return out
}

// --- small string/JSON helpers ---

// stringFieldRe builds a regex matching a top-level `"field": "value"` pair.
func stringFieldRe(field string) *regexp.Regexp {
	return regexp.MustCompile(`("` + regexp.QuoteMeta(field) + `"\s*:\s*")([^"]*)(")`)
}

// setStringField replaces only the FIRST `"field": "..."` value, preserving
// layout. package.json's canonical top-level "version"/"name" precede any nested
// occurrence, so replacing just the first avoids corrupting a same-named nested
// field (e.g. publishConfig.version, or a "version" key inside a dependency
// object) — ReplaceAllString would have rewritten every one of them.
func setStringField(text, field, value string) string {
	loc := stringFieldRe(field).FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	// Submatch indices: group 2 (the value) spans [loc[4], loc[5]); splice the
	// new value in literally so it is not subject to `$` expansion.
	return text[:loc[4]] + value + text[loc[5]:]
}

// setDependencyRange rewrites a `"<name>": "<range>"` entry's value wherever it
// appears (deps/devDeps/peerDeps). The dependency name is a full JSON key so the
// match is unambiguous.
func setDependencyRange(text, name, newRange string) string {
	re := regexp.MustCompile(`("` + regexp.QuoteMeta(name) + `"\s*:\s*")([^"]*)(")`)
	return re.ReplaceAllString(text, "${1}"+newRange+"${3}")
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// LocalOverlay is not implemented here: npm overrides or a workspaces array would serve; not implemented yet.
func (a *Adapter) LocalOverlay(ctx context.Context, req plugin.LocalOverlayRequest) (plugin.LocalOverlayResponse, error) {
	return plugin.LocalOverlayResponse{Skipped: true, Reason: "node: local overrides not implemented"}, nil
}
