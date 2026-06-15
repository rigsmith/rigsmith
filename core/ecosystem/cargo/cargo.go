// Package cargo implements the Rust/Cargo ecosystem adapter: it discovers
// Cargo.toml crate manifests, reads their [package] name/version/publish flag and
// intra-repo path dependencies, and rewrites the version (and dependency ranges)
// format-preservingly.
//
// Cargo manifests are TOML. To keep core dependency-free the manifest is
// hand-parsed (a simple table-aware line scan) rather than pulling in a TOML
// library — only a handful of well-known keys are read, and writes are targeted
// line/regex replacements that preserve layout.
package cargo

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Adapter is the in-process Cargo ecosystem adapter.
type Adapter struct{}

// New returns a Cargo adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// Info returns the Cargo adapter's identity and capabilities.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "cargo",
		DisplayName:      "Rust",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish, plugin.MethodArtifacts},
		ManifestPatterns: []string{"Cargo.toml"},
		DevCommands: map[string][]string{
			plugin.VerbBuild:  {"cargo", "build"},
			plugin.VerbTest:   {"cargo", "test"},
			plugin.VerbRun:    {"cargo", "run"},
			plugin.VerbFormat: {"cargo", "fmt"},
			plugin.VerbLint:   {"cargo", "clippy"},
			// `cargo check` type-checks without producing a binary — the canonical
			// fast feedback loop, mapped to rig's typecheck verb.
			plugin.VerbTypecheck: {"cargo", "check"},
			// Coverage uses cargo-llvm-cov (external subcommand); the CLI's coverage
			// command drives the --min/--open handling — see runCargoCoverage.
			plugin.VerbCoverage: {"cargo", "llvm-cov"},
			plugin.VerbInstall:  {"cargo", "fetch"},
			// Frozen install: --locked errors out rather than updating Cargo.lock.
			plugin.VerbCI:        {"cargo", "fetch", "--locked"},
			plugin.VerbAdd:       {"cargo", "add"},
			plugin.VerbUninstall: {"cargo", "remove"},
			// Requires cargo-outdated (external subcommand), like clippy/llvm-cov.
			plugin.VerbOutdated: {"cargo", "outdated"},
			plugin.VerbUpgrade:  {"cargo", "update"},
			plugin.VerbClean:    {"cargo", "clean"},
			plugin.VerbGlobal:   {"cargo", "install"},
		},
	}
}

// Detect reports whether a Cargo.toml exists at the repo root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	// Recursive: any crate (Cargo.toml with a [package]) under root counts, so the
	// ecosystem is detected even when crates live in a subdir of a polyglot repo.
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// crateManifest is the subset of a Cargo.toml the adapter reads: the [package]
// identity plus the raw dependency entries (name -> version range, "" when the
// dep is path-only) bucketed by table.
type crateManifest struct {
	name    string
	version string
	private bool // publish = false
	isCrate bool // has a [package] table (vs. a virtual [workspace]-only manifest)
	normal  map[string]string
	dev     map[string]string
	build   map[string]string
}

// Discover walks SourcePath (relative to RepoRoot; default ".") and returns one
// Package per Cargo.toml that declares a [package] (virtual workspace manifests
// are skipped), with intra-repo dependencies (those whose name matches another
// discovered crate) attached.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	// First pass: parse every Cargo.toml and collect the set of intra-repo names.
	type parsed struct {
		path string
		m    crateManifest
	}
	var all []parsed
	names := map[string]bool{}
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) != "Cargo.toml" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m := parseManifest(string(content))
		// Skip virtual manifests (a [workspace] with no [package]) for package
		// discovery; the walk has already found the member crates themselves.
		if !m.isCrate || m.name == "" {
			return nil
		}
		all = append(all, parsed{path: path, m: m})
		names[m.name] = true
		return nil
	})
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}

	// Second pass: build packages, keeping only intra-repo dependency edges.
	var resp plugin.DiscoverResponse
	for _, pr := range all {
		pkg := plugin.Package{
			Name:         pr.m.name,
			Version:      pr.m.version,
			Dir:          relTo(root, filepath.Dir(pr.path)),
			ManifestPath: relTo(root, pr.path),
			Private:      pr.m.private,
			Dependencies: intraRepoDeps(pr.m, names),
		}
		resp.Packages = append(resp.Packages, pkg)
	}
	return resp, nil
}

// SetVersion rewrites the [package] version and any named dependency ranges in the
// Cargo.toml, preserving formatting (a targeted, table-scoped line replace rather
// than re-serializing, which would drop comments and reorder keys).
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

	text = setPackageVersion(text, req.NewVersion)
	for _, du := range req.DependencyUpdates {
		text = setDependencyRange(text, du.Name, du.NewVersion)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// Publish runs `cargo publish` for the crate.
//
// Idempotency: crates.io offers no cheap pre-check, so we publish and inspect the
// failure — a stderr mentioning the version is "already" present (uploaded /
// exists) is the already-published case, which we report as Skipped; any other
// failure is returned as an error. A non-crates.io req.PackageSource is passed as
// --registry (the crates.io aliases "crates.io"/"crates" mean the default).
//
// Credentials: cargo uses the caller's token (`cargo login` / CARGO_REGISTRY_TOKEN),
// which we do not manage here.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	if req.Package.Private {
		return plugin.PublishResponse{Skipped: true, Message: "private"}, nil
	}

	dir := filepath.Join(req.RepoRoot, req.Package.Dir)

	args := []string{"publish"}
	if src := req.PackageSource; src != "" && src != "crates.io" && src != "crates" {
		args = append(args, "--registry", src)
	}

	if req.DryRun {
		// Report only — consistent with the other adapters' dry-run, so `--dry-run`
		// needs no toolchain and no clean git tree. (`cargo publish --dry-run` would
		// validate packaging but requires a committed tree / --allow-dirty.)
		return plugin.PublishResponse{
			Published: false,
			Message:   fmt.Sprintf("dry-run: would cargo publish %s@%s", req.Package.Name, req.Package.Version),
		}, nil
	}

	if _, stderr, err := runCmd(ctx, dir, "cargo", args...); err != nil {
		// crates.io rejects a re-publish of an existing version; that is our
		// idempotent skip, not a failure.
		if strings.Contains(strings.ToLower(stderr), "already") {
			return plugin.PublishResponse{Skipped: true, Message: "already published"}, nil
		}
		return plugin.PublishResponse{}, fmt.Errorf("cargo publish: %w", err)
	}

	return plugin.PublishResponse{
		Published: true,
		Message:   fmt.Sprintf("published %s@%s", req.Package.Name, req.Package.Version),
	}, nil
}

// Artifacts builds the crate tarball (`cargo package`) under req.OutputDir. The
// .crate is a registry artifact, so it is not attached to the GitHub release by
// default (Attach: false) — it ships to the registry via Publish.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	if req.Package.Private {
		return plugin.ArtifactsResponse{Skipped: true, Message: "private"}, nil
	}
	spec := req.Package.Name + "@" + req.Package.Version
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would cargo package %s", spec)}, nil
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("cargo package: mkdir %s: %w", req.OutputDir, err)
	}
	dir := filepath.Join(req.RepoRoot, req.Package.Dir)
	// --no-verify keeps it fast (skips the compile-the-packaged-crate check);
	// --allow-dirty tolerates the uncommitted version bump made earlier in the
	// release; --target-dir lands the .crate under OutputDir/package/.
	if _, _, err := runCmd(ctx, dir, "cargo", "package", "--no-verify", "--allow-dirty", "--target-dir", req.OutputDir); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("cargo package: %w", err)
	}
	crate := filepath.Join(req.OutputDir, "package", req.Package.Name+"-"+req.Package.Version+".crate")
	return plugin.ArtifactsResponse{
		Built:     true,
		Artifacts: []plugin.Artifact{{Path: crate, Kind: plugin.ArtifactPackage, Attach: false}},
		Message:   "packaged " + spec,
	}, nil
}

// runCmd runs name+args in dir ("" for the current directory) and returns the
// captured stdout/stderr. On a non-zero exit the error wraps stderr for
// diagnostics.
func runCmd(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
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
// crate, tagging each with its kind (dev-dependencies -> dev, the rest -> normal;
// Cargo has no peer deps) and the version range as written ("" for path-only deps).
func intraRepoDeps(m crateManifest, known map[string]bool) []plugin.Dependency {
	var deps []plugin.Dependency
	collect := func(entries map[string]string, kind plugin.DependencyKind) {
		// Sort for deterministic output (map iteration is randomized).
		dnames := make([]string, 0, len(entries))
		for name := range entries {
			dnames = append(dnames, name)
		}
		sortStrings(dnames)
		for _, name := range dnames {
			if known[name] {
				deps = append(deps, plugin.Dependency{Name: name, Kind: kind, Range: entries[name]})
			}
		}
	}
	collect(m.normal, plugin.DepNormal)
	collect(m.dev, plugin.DepDev)
	collect(m.build, plugin.DepNormal)
	return deps
}

// --- minimal hand-rolled TOML reading ---

var (
	// tableRe matches a table header line: `[package]`, `[dependencies]`,
	// `[dev-dependencies]`, etc. (sub-tables like `[dependencies.foo]` included).
	tableRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	// keyStringRe matches `key = "value"` (the only value shape we need to read).
	keyStringRe = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*"([^"]*)"\s*$`)
	// keyBoolRe matches `key = true|false`.
	keyBoolRe = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*(true|false)\s*$`)
	// depVersionRe pulls a `version = "X"` field out of an inline dependency table
	// (e.g. `foo = { version = "1.2", path = "../foo" }`).
	depVersionRe = regexp.MustCompile(`\bversion\s*=\s*"([^"]*)"`)
)

// parseManifest hand-parses the subset of a Cargo.toml the adapter needs: the
// [package] name/version/publish and the dependency entries by table.
func parseManifest(text string) crateManifest {
	m := crateManifest{
		normal: map[string]string{},
		dev:    map[string]string{},
		build:  map[string]string{},
	}
	section := "" // current table header, lowercased

	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if h := tableRe.FindStringSubmatch(trimmed); h != nil {
			section = strings.TrimSpace(h[1])
			if section == "package" {
				m.isCrate = true
			}
			continue
		}

		switch section {
		case "package":
			if kv := keyStringRe.FindStringSubmatch(line); kv != nil {
				switch kv[1] {
				case "name":
					m.name = kv[2]
				case "version":
					m.version = kv[2]
				}
			} else if kb := keyBoolRe.FindStringSubmatch(line); kb != nil {
				if kb[1] == "publish" && kb[2] == "false" {
					m.private = true
				}
			}
		case "dependencies", "dev-dependencies", "build-dependencies":
			if name, ver, ok := parseDepLine(line); ok {
				bucketFor(&m, section)[name] = ver
			}
		}
	}
	return m
}

// bucketFor returns the dependency map for a [*dependencies] table.
func bucketFor(m *crateManifest, section string) map[string]string {
	switch section {
	case "dev-dependencies":
		return m.dev
	case "build-dependencies":
		return m.build
	default:
		return m.normal
	}
}

// parseDepLine reads one dependency entry, returning its crate name and version
// range ("" when path-only). It handles the three common shapes:
//
//	foo = "1.2"
//	foo = { path = "../foo" }
//	foo = { version = "1.2", path = "../foo" }
func parseDepLine(line string) (name, version string, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(line[:eq])
	if name == "" || !isCrateName(name) {
		return "", "", false
	}
	rhs := strings.TrimSpace(line[eq+1:])
	switch {
	case strings.HasPrefix(rhs, `"`):
		// Plain `foo = "1.2"`.
		if v := strings.Trim(rhs, `"`); v != rhs {
			return name, v, true
		}
		return name, "", true
	case strings.HasPrefix(rhs, "{"):
		// Inline table — a version field is optional (path-only deps have none).
		if mv := depVersionRe.FindStringSubmatch(rhs); mv != nil {
			return name, mv[1], true
		}
		return name, "", true
	}
	return "", "", false
}

// isCrateName reports whether s looks like a bare crate-name key (rejecting
// dotted keys such as a stray `dependencies.foo` line).
func isCrateName(s string) bool {
	for _, r := range s {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return s != ""
}

// --- targeted, table-scoped writes ---

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
// appears in a [*dependencies] table, handling both the plain-string and inline-
// table shapes. Path-only deps (no version field) are left as-is.
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

// rewriteInTable scans text line by line, invoking fn on each line that belongs
// to the named table (between its `[table]` header and the next header), and
// substitutes any line fn rewrites. Layout — including the line ending — is
// preserved.
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

// --- small helpers (mirrors of the node/dotnet adapters) ---

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
