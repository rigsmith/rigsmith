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
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

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
// "public"/"restricted". A URL-shaped req.PackageSource is passed as --registry.
//
// Credentials: npm uses the caller's npm auth (~/.npmrc / NPM_TOKEN), which we do
// not manage here.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	if req.Package.Private {
		return plugin.PublishResponse{Skipped: true, Message: "private"}, nil
	}

	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	spec := req.Package.Name + "@" + req.Package.Version

	// Pre-check: a clean exit echoing the requested version means it already exists.
	// A non-zero exit (unpublished version / network) is treated as "not present"
	// and we proceed to publish, where npm will surface any real failure.
	if out, _, err := runCmd(ctx, dir, "npm", "view", spec, "version"); err == nil {
		if strings.TrimSpace(out) == req.Package.Version {
			return plugin.PublishResponse{Skipped: true, Message: "already published"}, nil
		}
	}

	if req.DryRun {
		return plugin.PublishResponse{
			Published: false,
			Message:   fmt.Sprintf("dry-run: would npm publish %s", spec),
		}, nil
	}

	access := req.Access
	if access != "public" && access != "restricted" {
		access = "restricted"
	}
	args := []string{"publish", "--access", access}
	if strings.HasPrefix(req.PackageSource, "http") {
		args = append(args, "--registry", req.PackageSource)
	}

	// Resolve the publish credential. With nothing supplied, npm uses the
	// caller's ambient auth (~/.npmrc / NPM_TOKEN) and we touch nothing. With a
	// token (an engine-resolved secret ref, or one we mint via OIDC), write it
	// to a temp npmrc seeded from the existing one (so scopes/registries
	// survive) and point npm at it via NPM_CONFIG_USERCONFIG — env config layers
	// above project/user .npmrc without putting the token on the command line.
	env := os.Environ()
	authNote := ""
	switch {
	case req.Auth != nil && req.Auth.Token != "":
		authEnv, cleanup, err := npmAuthConfig(req.Auth.Token, req.PackageSource)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		defer cleanup()
		env = authEnv
		if req.Auth.Method == "secret-ref" {
			authNote = " (auth via secret reference)"
		}

	case req.OIDC:
		token, err := oidcPublishToken(ctx, req.Package.Name, req.PackageSource)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		authEnv, cleanup, err := npmAuthConfig(token, req.PackageSource)
		if err != nil {
			return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
		}
		defer cleanup()
		env = authEnv

		// Provenance attestation rides on top of OIDC auth, but needs the npm
		// CLI's attestation machinery (npm ≥ 11.5.1). When the runner is too old
		// we still publish — auth never depends on the npm version, only the
		// attestation does.
		if npmSupportsProvenance(ctx) {
			args = append(args, "--provenance")
			authNote = " (auth via OIDC trusted publishing, provenance attested)"
		} else {
			authNote = " (auth via OIDC trusted publishing; provenance skipped — needs npm ≥ 11.5.1)"
		}
	}

	if _, _, err := runCmdEnv(ctx, dir, env, "npm", args...); err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("npm publish: %w", err)
	}

	return plugin.PublishResponse{Published: true, Message: "published " + spec + authNote}, nil
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
	if req.Package.Private {
		return plugin.ArtifactsResponse{Skipped: true, Message: "private"}, nil
	}
	spec := req.Package.Name + "@" + req.Package.Version
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would npm pack %s", spec)}, nil
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("npm pack: mkdir %s: %w", req.OutputDir, err)
	}
	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
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
				"CI must grant `id-token: write` and run actions/setup-node with registry-url; provenance needs npm ≥ 11.5.1",
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
		Notes: []string{"publishes to the npm registry (set npm.oidc to enable tokenless OIDC publishing)"},
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
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr))
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
				candidate := filepath.Join(dir, seg)
				if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
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
		if e.IsDir() && !walkutil.SkippedDir(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
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
