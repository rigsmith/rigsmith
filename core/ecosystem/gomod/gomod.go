// Package gomod implements the Go ecosystem adapter: it discovers go.mod modules
// and their intra-repo require edges.
//
// Go modules have NO version field in go.mod — the version source is git tags of
// the form `module/vX.Y.Z` (or `vX.Y.Z` for a root module). Per decision Q2,
// Discover reads the latest matching git tag as authoritative (option a), with an
// optional `// rigsmith:version X.Y.Z` comment as a secondary fallback (option b)
// for un-tagged repos. SetVersion still writes that comment as a record; the real
// release operation (creating + pushing the tag) belongs to `tag`/`publish`,
// which are still stubbed here — see Publish's TODO.
package gomod

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/semver"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Adapter is the in-process Go ecosystem adapter.
type Adapter struct{}

// New returns a Go adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// defaultVersion is used when no `// rigsmith:version` annotation is present.
const defaultVersion = "0.0.0"

var (
	// moduleRe captures the module path and any trailing `// rigsmith:version X`.
	// A foreign trailing comment (e.g. `// Deprecated: use bar`, a valid go.mod
	// directive) is matched and ignored via the final `(?://.*)?` so the module is
	// still discovered — earlier the line had to end right after the (optional)
	// rigsmith annotation, so any other comment made the whole line fail to match
	// and the module was silently skipped. `.` does not cross the newline under
	// (?m), so the match stays on the module line for SetVersion's in-place rewrite.
	moduleRe         = regexp.MustCompile(`(?m)^module[ \t]+(\S+)(?:[ \t]+//[ \t]*rigsmith:version[ \t]+(\S+))?[ \t]*(?://.*)?$`)
	versionCommentRe = regexp.MustCompile(`//\s*rigsmith:version\s+\S+`)
)

// Info returns the Go adapter's identity and capabilities.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "go",
		DisplayName:      "Go",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish, plugin.MethodArtifacts, plugin.MethodReleaseInit},
		ManifestPatterns: []string{"go.mod"},
		DevCommands: map[string][]string{
			plugin.VerbBuild:  {"go", "build", "./..."},
			plugin.VerbTest:   {"go", "test", "./..."},
			plugin.VerbRun:    {"go", "run", "."},
			plugin.VerbFormat: {"gofmt", "-l", "-w", "."},
			// Go folds linting and type-checking into the compiler + `go vet`,
			// which type-checks the program as part of its analysis. There is no
			// separate type-only pass, so both lint and typecheck surface the
			// canonical `go vet ./...` — giving `rig lint`/`rig check` a sensible
			// Go behavior for cross-ecosystem muscle memory.
			plugin.VerbLint:      {"go", "vet", "./..."},
			plugin.VerbTypecheck: {"go", "vet", "./..."},
			plugin.VerbCoverage:  {"go", "test", "-cover", "./..."},
			plugin.VerbInstall:   {"go", "mod", "download"},
			// Go has no distinct frozen-install: module downloads are checksum-
			// verified against go.sum, so `ci` mirrors `install`.
			plugin.VerbCI:       {"go", "mod", "download"},
			plugin.VerbAdd:      {"go", "get"},
			plugin.VerbUpgrade:  {"go", "get", "-u", "./..."},
			plugin.VerbOutdated: {"go", "list", "-m", "-u", "all"},
			plugin.VerbClean:    {"go", "clean"},
			plugin.VerbGlobal:   {"go", "install"},
			// `go run pkg@latest` runs a tool once without installing it — Go's
			// equivalent of npx/dnx. The caller appends the package@version.
			plugin.VerbDlx: {"go", "run"},
		},
	}
}

// Detect reports whether a go.mod exists at the repo root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	// Recursive: any go.mod under root counts, so a go.work monorepo (no root
	// go.mod) and a polyglot repo with Go in a subdir are both detected.
	resp, err := a.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		return false, err
	}
	return len(resp.Packages) > 0, nil
}

// Discover walks for go.mod files and returns one Package each, with intra-repo
// require edges (those whose module path matches another discovered module).
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	// First pass: parse every go.mod and collect the set of module paths.
	type parsed struct {
		path    string
		module  string
		version string
		require map[string]string // module path -> required version
	}
	var all []parsed
	modules := map[string]bool{}
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if filepath.Base(path) != "go.mod" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mod, ver := parseModule(string(content))
		if mod == "" {
			return nil
		}
		all = append(all, parsed{path: path, module: mod, version: ver, require: parseRequires(string(content))})
		modules[mod] = true
		return nil
	})
	if err != nil {
		return plugin.DiscoverResponse{}, err
	}

	// Second pass: build packages with only intra-repo require edges.
	var resp plugin.DiscoverResponse
	for _, pr := range all {
		var deps []plugin.Dependency
		names := make([]string, 0, len(pr.require))
		for name := range pr.require {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			if modules[name] && name != pr.module {
				deps = append(deps, plugin.Dependency{Name: name, Kind: plugin.DepNormal, Range: pr.require[name]})
			}
		}
		dir := relTo(root, filepath.Dir(pr.path))
		// Version resolution (decision Q2 → option a, git-tag native): the latest
		// matching git tag is the released version. The `// rigsmith:version` comment
		// is a fallback for repos without tags yet — but it can also be AHEAD of the
		// tag: the `version` step bumps the comment to the pending release before its
		// tag exists. So take whichever is higher, so the `tag` step can advance past
		// the last release instead of re-reading it (which would never tag a 2nd+
		// release). The tag wins on a tie or a missing/older comment.
		version := pr.version
		if tagVer, ok := gitutil.LatestModuleVersion(ctx, root, dir); ok {
			version = higherVersion(pr.version, tagVer)
		}
		resp.Packages = append(resp.Packages, plugin.Package{
			Name:         pr.module,
			Version:      version,
			Dir:          dir,
			ManifestPath: relTo(root, pr.path),
			Dependencies: deps,
		})
	}
	return resp, nil
}

// SetVersion updates (or creates) the `// rigsmith:version` comment on the module
// line as a placeholder.
//
// TODO: the real Go release operation is creating + pushing a git tag
// (module/vX.Y.Z), not editing go.mod. See the package doc.
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	path := filepath.Join(req.RepoRoot, req.Package.ManifestPath)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated := setVersionComment(string(content), req.NewVersion)
	return os.WriteFile(path, []byte(updated), 0o644)
}

// Publish for a Go module is a no-op at the registry level: there is no registry
// push — a Go module is "published" by creating and pushing a git tag
// (module/vX.Y.Z), which the module proxy then serves. The shiprig `publish`
// command performs that tag + push in its tagging phase (shared with `tag`), so
// this adapter reports the version as handled-by-tag rather than pushing here.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "published via git tag (publish tagging phase)"}, nil
}

// Artifacts builds the module's distributable binaries with goreleaser when a
// .goreleaser.yaml is present (the cross-platform binary story for Go). A Go
// module with no goreleaser config is "published" by its git tag and has no
// binaries to ship, so it is skipped. The produced archives + checksums are
// release assets (Attach: true). goreleaser builds into the repo's dist/.
//
// The build runs *before* any tag exists (the release pipeline's `build` step is
// early, so a broken build never reaches publish). A Go version normally comes
// from the git tag, so we inject the already-bumped version via
// GORELEASER_CURRENT_TAG and skip goreleaser's tag-at-HEAD validation — making
// the build independent of tag ordering. A rehearse build (--snapshot) derives
// its own pseudo-version and needs neither.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	if goreleaserConfig(req.RepoRoot) == "" {
		return plugin.ArtifactsResponse{Skipped: true, Message: "no .goreleaser.yaml; Go modules ship via git tag"}, nil
	}
	tag := moduleTag(req.Package.Dir, req.Package.Version)
	// A real build injects the bumped version (no tag needed yet) and skips
	// goreleaser's tag-at-HEAD validation; a rehearse uses --snapshot (its own
	// pseudo-version). The dry-run message is derived from the very args run
	// below, so intent and execution can't drift.
	args := []string{"release", "--clean", "--skip=publish,validate"}
	env := append(os.Environ(), "GORELEASER_CURRENT_TAG="+tag)
	note := " (GORELEASER_CURRENT_TAG=" + tag + ")"
	if req.Snapshot {
		args = []string{"release", "--clean", "--snapshot"}
		env = os.Environ()
		note = ""
	}
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: "dry-run: would run goreleaser " + strings.Join(args, " ") + note}, nil
	}
	if _, err := exec.LookPath("goreleaser"); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("goreleaser not found on PATH (needed to build Go binaries; see https://goreleaser.com): %w", err)
	}
	if _, _, err := runCmd(ctx, req.RepoRoot, env, "goreleaser", args...); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("goreleaser: %w", err)
	}
	// goreleaser writes to its configured dist dir (default <repo>/dist); the
	// release pipeline passes that as OutputDir, so collect from there.
	arts, err := collectDist(req.OutputDir)
	if err != nil {
		return plugin.ArtifactsResponse{}, err
	}
	return plugin.ArtifactsResponse{Built: true, Artifacts: arts, Message: "built binaries via goreleaser"}, nil
}

// moduleTag is the version tag for a Go module: vX.Y.Z at the repo root, or
// dir/vX.Y.Z for a sub-module (matching gitutil.PackageTag's Go convention).
func moduleTag(dir, version string) string {
	if dir == "" || dir == "." {
		return "v" + version
	}
	return dir + "/v" + version
}

// goreleaserConfig returns the path to a goreleaser config in root, or "".
func goreleaserConfig(root string) string {
	for _, n := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		if p := filepath.Join(root, n); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ReleaseInit declares Go's release prerequisites. Unlike the other ecosystems,
// Go has no native single-command artifact producer: cross-platform binaries are
// built with goreleaser, which needs a .goreleaser.yaml. So when the repo builds
// binaries (any `package main` under the discovered modules) and no config is
// present, ReleaseInit hands back a starter templated from those mains — keeping
// the goreleaser knowledge inside this plugin. A binary-less module ships by git
// tag and needs no build config. GITHUB_TOKEN covers creating the release and
// uploading the built archives.
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	resp := plugin.ReleaseInitResponse{
		Tokens: []plugin.TokenSpec{{
			EnvVar: "GITHUB_TOKEN",
			For:    "create the GitHub release and upload binary assets",
			URL:    "https://github.com/settings/tokens (or run `gh auth login`)",
		}},
	}

	mains := discoverMains(req.RepoRoot, req.Packages)
	if len(mains) == 0 {
		resp.Notes = []string{"Go modules release by git tag (no binaries to build)"}
		return resp, nil
	}

	names := make([]string, len(mains))
	for i, m := range mains {
		names[i] = m.binary
	}
	resp.Notes = []string{"ships cross-platform binaries via goreleaser: " + strings.Join(names, ", ")}

	bc := &plugin.BuildConfigSpec{Path: ".goreleaser.yaml", Tool: "goreleaser"}
	if existing := goreleaserConfig(req.RepoRoot); existing != "" {
		bc.Path = filepath.Base(existing)
		bc.Present = true
	} else {
		bc.Content = goreleaserStarter(filepath.Base(req.RepoRoot), mains)
	}
	resp.BuildConfig = bc
	return resp, nil
}

// mainPkg is one buildable binary: its goreleaser build id/binary name and the
// `main:` path relative to the repo root (where .goreleaser.yaml lives).
type mainPkg struct {
	binary string // "rig" (base of the main package dir)
	main   string // "./cmd/rig"
}

// discoverMains finds every `package main` directory under the discovered
// modules — the binaries a goreleaser config would build. Results are unique by
// main path and sorted for a stable starter file.
func discoverMains(root string, pkgs []plugin.Package) []mainPkg {
	seen := map[string]bool{}
	var mains []mainPkg
	for _, p := range pkgs {
		modDir := filepath.Join(root, p.Dir)
		_ = walkutil.Walk(modDir, func(path string, d fs.DirEntry) error {
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if !declaresMain(path) {
				return nil
			}
			dir := filepath.Dir(path)
			rel, err := filepath.Rel(root, dir)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			mainPath := "./" + rel
			if rel == "." {
				mainPath = "."
			}
			if seen[mainPath] {
				return nil
			}
			seen[mainPath] = true
			mains = append(mains, mainPkg{binary: filepath.Base(dir), main: mainPath})
			return nil
		})
	}
	sort.Slice(mains, func(i, j int) bool { return mains[i].main < mains[j].main })
	return mains
}

// packageMainRe matches a `package main` clause at the start of a line.
var packageMainRe = regexp.MustCompile(`(?m)^package[ \t]+main\b`)

// declaresMain reports whether a .go file declares `package main`.
func declaresMain(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return packageMainRe.Match(b)
}

// goreleaserStarter renders a minimal, valid goreleaser config that builds one
// block per discovered binary (linux/darwin/windows × amd64/arm64), archives
// each, and writes a checksums file. It is intentionally a starting point —
// ldflags version seams, a Homebrew tap, and the release repo slug are left as
// commented TODOs for the user to tune, then `goreleaser check`.
func goreleaserStarter(projectName string, mains []mainPkg) string {
	var b strings.Builder
	b.WriteString("# .goreleaser.yaml — generated by `shiprig init`. Tune, then run `goreleaser check`.\n")
	b.WriteString("# One build + archive block per binary discovered under the module's main packages.\n")
	b.WriteString("version: 2\n\n")
	b.WriteString("project_name: " + projectName + "\n\n")

	b.WriteString("builds:\n")
	for _, m := range mains {
		b.WriteString("  - id: " + m.binary + "\n")
		b.WriteString("    main: " + m.main + "\n")
		b.WriteString("    binary: " + m.binary + "\n")
		b.WriteString("    env:\n      - CGO_ENABLED=0\n")
		b.WriteString("    goos: [linux, darwin, windows]\n")
		b.WriteString("    goarch: [amd64, arm64]\n")
		b.WriteString("    ldflags:\n      - -s -w  # add `-X <pkg>.version={{.Version}}` to stamp a version seam\n")
	}
	b.WriteString("\n")

	b.WriteString("archives:\n")
	for _, m := range mains {
		b.WriteString("  - id: " + m.binary + "\n")
		b.WriteString("    ids: [" + m.binary + "]\n")
		b.WriteString("    name_template: \"" + m.binary + "_{{ .Version }}_{{ .Os }}_{{ .Arch }}\"\n")
		b.WriteString("    formats: [tar.gz]\n")
		b.WriteString("    format_overrides:\n      - goos: windows\n        formats: [zip]\n")
	}
	b.WriteString("\n")

	b.WriteString("checksum:\n  name_template: \"checksums.txt\"\n\n")
	b.WriteString("# Uncomment and set your repo slug so goreleaser knows where the release lives.\n")
	b.WriteString("# (shiprig's pipeline creates the release and uploads these archives.)\n")
	b.WriteString("# release:\n#   github:\n#     owner: your-org\n#     name: " + projectName + "\n")
	return b.String()
}

// collectDist gathers the release assets goreleaser wrote to dist/: the archives
// (.tar.gz/.zip) and the checksums file. The raw per-binary executables and
// goreleaser's metadata.json/artifacts.json are left out — the archives ship.
func collectDist(dist string) ([]plugin.Artifact, error) {
	entries, err := os.ReadDir(dist)
	if err != nil {
		return nil, fmt.Errorf("reading goreleaser dist %s: %w", dist, err)
	}
	var arts []plugin.Artifact
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dist, name)
		switch {
		case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip"):
			arts = append(arts, plugin.Artifact{Path: path, Kind: plugin.ArtifactArchive, Attach: true})
		case strings.Contains(strings.ToLower(name), "checksum"):
			arts = append(arts, plugin.Artifact{Path: path, Kind: plugin.ArtifactChecksum, Attach: true})
		}
	}
	return arts, nil
}

// runCmd runs name with args in dir, returning stdout/stderr for diagnostics.
// A non-nil env replaces the child's environment (pass os.Environ()-based slices
// to extend it); nil inherits the parent's environment.
func runCmd(ctx context.Context, dir string, env []string, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
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

// parseModule returns the module path and the optional `// rigsmith:version`
// value (defaultVersion when the annotation is absent).
func parseModule(text string) (module, version string) {
	m := moduleRe.FindStringSubmatch(text)
	if m == nil {
		return "", ""
	}
	version = defaultVersion
	if m[2] != "" {
		version = m[2]
	}
	return m[1], version
}

// higherVersion returns the greater of the `// rigsmith:version` comment value
// and the latest git-tag version. The git tag is the released version, but the
// comment is bumped to the pending release before that tag exists, so the tag
// step needs the higher of the two to advance past the last release. The tag
// wins on a tie; an unparseable comment yields the tag (the historical behavior).
func higherVersion(commentVer, tagVer string) string {
	cv, cok := semver.Parse(commentVer)
	if !cok {
		return tagVer
	}
	tv, tok := semver.Parse(tagVer)
	if !tok || semver.Compare(cv, tv) > 0 {
		return commentVer
	}
	return tagVer
}

// parseRequires extracts the require edges (module path -> version) from both the
// single-line `require x v` form and the `require ( ... )` block form.
func parseRequires(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	inBlock := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
				continue
			}
			addRequire(out, line)
		case line == "require (":
			inBlock = true
		case strings.HasPrefix(line, "require ("):
			inBlock = true
		case strings.HasPrefix(line, "require "):
			addRequire(out, strings.TrimSpace(strings.TrimPrefix(line, "require")))
		}
	}
	return out
}

// addRequire parses a `<module> <version>` pair into the map.
func addRequire(out map[string]string, line string) {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		out[fields[0]] = fields[1]
	}
}

// setVersionComment replaces the `// rigsmith:version` annotation on the module
// line, inserting one when absent.
func setVersionComment(text, newVersion string) string {
	loc := moduleRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	line := text[loc[0]:loc[1]]
	annotation := "// rigsmith:version " + newVersion
	var newLine string
	switch {
	case versionCommentRe.MatchString(line):
		// Update the existing rigsmith annotation in place.
		newLine = versionCommentRe.ReplaceAllString(line, annotation)
	case strings.Contains(line, "//"):
		// The module line already carries a foreign comment (e.g. a
		// `// Deprecated:` directive). go.mod allows only one comment per line and
		// the authoritative version source is the git tag, so leave the line intact
		// rather than clobber the existing comment (or bury the annotation behind it
		// where parseModule could no longer read it back).
		newLine = line
	default:
		newLine = strings.TrimRight(line, " \t") + " " + annotation
	}
	return text[:loc[0]] + newLine + text[loc[1]:]
}

func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
