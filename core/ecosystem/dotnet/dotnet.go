// Package dotnet implements the .NET ecosystem adapter: it discovers .csproj
// projects, resolves their version (inline or from a shared Directory.Build.props),
// and stamps new versions back into the owning file format-preservingly.
//
// It is a faithful port of net-changesets' CsProjectsRepository +
// ProjectVersionResolver. Like the C# original it parses with regex rather than a
// strict XML reader (namespace-agnostic, format-preserving on write), since the
// only goal is reading/rewriting a handful of well-known elements.
package dotnet

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/cmderr"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/walkutil"
)

// Adapter is the in-process .NET ecosystem adapter.
type Adapter struct{}

// New returns a .NET adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// propsFileName is the shared props file MSBuild walks ancestors for.
const propsFileName = "Directory.Build.props"

// Element-matching regexes. They are namespace-agnostic and tolerate attributes
// (e.g. Condition) on the element, matching the C# Descendants().LocalName scan.
var (
	versionRe       = regexp.MustCompile(`(?s)(<Version>)(.*?)(</Version>)`)
	versionPrefixRe = regexp.MustCompile(`(?s)(<VersionPrefix>)(.*?)(</VersionPrefix>)`)
	packageIDRe     = regexp.MustCompile(`(?s)<PackageId>(.*?)</PackageId>`)
	isPackableRe    = regexp.MustCompile(`(?s)<IsPackable[^>]*>(.*?)</IsPackable>`)
	// A project whose version is MinVer's to compute says so in one of two
	// ways: the package reference, or any of the MinVer* properties that tune
	// it (MinVerTagPrefix, MinVerMinimumMajorMinor, …) in a props file.
	minVerRe        = regexp.MustCompile(`<PackageReference[^>]*\bInclude\s*=\s*"MinVer"|<MinVer[A-Za-z]*[\s>]`)
	projectRefRe    = regexp.MustCompile(`<ProjectReference[^>]*\bInclude\s*=\s*"([^"]*)"`)
	packageRefRe    = regexp.MustCompile(`<PackageReference[^>]*\bInclude\s*=\s*"([^"]*)"`)
	propertyGroupRe = regexp.MustCompile(`<PropertyGroup[^>]*>`)
)

// versionElement records which element holds a project's bumpable number.
type versionElement int

const (
	elemVersion versionElement = iota
	elemVersionPrefix
)

func (e versionElement) tag() string {
	if e == elemVersionPrefix {
		return "VersionPrefix"
	}
	return "Version"
}

// Info returns the .NET adapter's identity and capabilities.
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:       plugin.APIVersion,
		ID:               "dotnet",
		DisplayName:      ".NET",
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish, plugin.MethodArtifacts, plugin.MethodReleaseInit, plugin.MethodLocalOverlay},
		ManifestPatterns: []string{"*.csproj"},
		DevCommands: map[string][]string{
			plugin.VerbBuild:  {"dotnet", "build"},
			plugin.VerbTest:   {"dotnet", "test"},
			plugin.VerbRun:    {"dotnet", "run"},
			plugin.VerbFormat: {"dotnet", "format"},
			// Roslyn analyzers (e.g. SonarAnalyzer.CSharp, Meziantou.Analyzer)
			// have no standalone CLI; `dotnet format analyzers` runs whatever
			// analyzers the project references. --verify-no-changes makes it a
			// non-mutating check that exits non-zero on findings.
			plugin.VerbLint:     {"dotnet", "format", "analyzers", "--verify-no-changes"},
			plugin.VerbCoverage: {"dotnet", "test", "--collect:XPlat Code Coverage"},
			plugin.VerbInstall:  {"dotnet", "restore"},
			// Frozen restore: --locked-mode fails if it would change packages.lock.json.
			plugin.VerbCI:        {"dotnet", "restore", "--locked-mode"},
			plugin.VerbAdd:       {"dotnet", "add", "package"},
			plugin.VerbUninstall: {"dotnet", "remove", "package"},
			plugin.VerbOutdated:  {"dotnet", "list", "package", "--outdated"},
			plugin.VerbClean:     {"dotnet", "clean"},
			plugin.VerbGlobal:    {"dotnet", "tool", "install", "--global"},
			plugin.VerbDlx:       {"dnx"},
		},
	}
}

// ProjectFileExts are the MSBuild project files a .NET project is found by.
// C# is not the only .NET language, and a repo of F# or VB projects is a .NET
// repo by every other measure.
var ProjectFileExts = []string{".csproj", ".fsproj", ".vbproj"}

// IsProjectFile reports whether name is an MSBuild project file.
//
// Exported because rig's own .NET discovery needs the identical answer: this
// predicate decides whether a repo is .NET at all (Detect, below), and that runs
// as a GATE before discovery. Any disagreement between the two is silent — the
// repo is simply reported as having no projects.
func IsProjectFile(name string) bool {
	for _, ext := range ProjectFileExts {
		if len(name) >= len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext) {
			return true
		}
	}
	return false
}

// SkippedScanDirs are the directories .NET discovery never descends into: build
// output and dependency trees, which hold copies of project files.
//
// `vendor` is deliberately ABSENT, unlike the shared walkutil skip set: a
// vendored *.csproj is a first-class build input in .NET and solutions routinely
// list them, so skipping it would make a repo whose only project is vendored
// look like no .NET repo at all.
var SkippedScanDirs = []string{"bin", "obj", ".git", "node_modules"}

// Detect reports whether any MSBuild project exists under root.
//
// Deliberately not walkutil.Walk: that skips `vendor` for every ecosystem, which
// is right for Go and wrong here (see SkippedScanDirs). Since Detect gates
// whether rig's .NET discovery runs at all, a false negative here hides the
// whole repo — an F#-only or vendored-only project set would report no projects
// rather than the ones it has.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // unreadable subtree — detection is best-effort
		}
		if d.IsDir() {
			for _, skip := range SkippedScanDirs {
				if strings.EqualFold(d.Name(), skip) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if IsProjectFile(d.Name()) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// Discover walks SourcePath (relative to RepoRoot; default ".") and returns one
// Package per .csproj that declares a version somewhere up its ancestry.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	root := req.RepoRoot
	source := req.SourcePath
	if source == "" {
		source = "."
	}
	scanRoot := filepath.Join(root, source)

	var resp plugin.DiscoverResponse
	err := walkutil.Walk(scanRoot, func(path string, d fs.DirEntry) error {
		if !strings.HasSuffix(path, ".csproj") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)

		// Resolve the version: inline first, else from an ancestor props file. A
		// project with no version anywhere is skipped (matches the C# original) —
		// unless it is packable all the same, its number computed at build time
		// (MinVer from git tags, a CI-stamped build), or the caller asked for
		// identity rather than release readiness. Either way it comes back with
		// an empty Version: the release records the number it computes for such
		// a package beside the changesets, since the tree holds none.
		resolved, ok := resolveVersion(path, text)
		if !ok {
			if !req.IncludeUnversioned && !packable(path, text) {
				return nil
			}
			resolved = resolvedVersion{}
		}

		name := packageID(text)
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(path), ".csproj")
		}

		pkg := plugin.Package{
			Name:         name,
			Version:      resolved.version,
			Dir:          relTo(root, filepath.Dir(path)),
			ManifestPath: relTo(root, path),
			Dependencies: projectReferences(text),
		}
		if req.IncludeRegistrySiblings {
			pkg.Dependencies = append(pkg.Dependencies, packageReferences(text)...)
		}
		// When the version comes from a shared props file the package's VersionFile
		// differs from its manifest — this is what drives lockstep grouping.
		if resolved.shared {
			pkg.VersionFile = relTo(root, resolved.filePath)
		}
		resp.Packages = append(resp.Packages, pkg)
		return nil
	})
	return resp, err
}

// SetVersion stamps NewVersion into the package's version file (VersionFile when
// set, else ManifestPath), format-preserving. If the element is absent, it is
// inserted into the first <PropertyGroup>.
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

	// Pick the element already present (Version wins over VersionPrefix, like MSBuild).
	// Scoped to <PropertyGroup> for the same reason as fromText: an <ItemGroup> item's
	// <Version> metadata is not the project's version and must not decide this.
	elem := elemVersion
	if findInPropertyGroup(text, versionRe) == nil && findInPropertyGroup(text, versionPrefixRe) != nil {
		elem = elemVersionPrefix
	}

	updated, err := writeVersion(text, elem, req.NewVersion)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// Publish packs the project and pushes the resulting .nupkg to a NuGet feed.
//
// Idempotency rides on `dotnet nuget push --skip-duplicate`: a version already
// present on the feed is a no-op on the server's side and a zero exit here, so no
// separate registry query is needed. Because the exit code does not distinguish
// "pushed" from "skipped duplicate", a successful push is reported as Published.
//
// Credentials: the API key comes from the NUGET_API_KEY env var when set; when it
// is unset we still run, letting `dotnet` fall back to stored feed credentials.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	if req.Package.Private {
		return plugin.PublishResponse{Skipped: true, Message: "private"}, nil
	}

	source := req.PackageSource
	if source == "" {
		source = "nuget"
	}

	// Dry-run reports only (no pack), so `--dry-run` needs no toolchain — uniform
	// with the other adapters.
	if req.DryRun {
		return plugin.PublishResponse{
			Published: false,
			Message:   fmt.Sprintf("dry-run: would pack+push %s@%s to %s", req.Package.Name, req.Package.Version, source),
		}, nil
	}

	// Pack into a throwaway directory so the .nupkg never lands in the work tree.
	tmpDir, err := os.MkdirTemp("", "rigsmith-nupkg-*")
	if err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("dotnet publish: mkdir temp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	manifest := filepath.Join(req.RepoRoot, req.Package.ManifestPath)
	if _, _, err := packRunner(ctx, "", "dotnet", packArgs(manifest, tmpDir, req.Package.Version)...); err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("dotnet pack: %w", err)
	}

	// The PackageId is req.Package.Name and the version is req.Package.Version, so
	// the produced artifact is deterministically named.
	nupkg := filepath.Join(tmpDir, req.Package.Name+"."+req.Package.Version+".nupkg")

	// Resolve the API key. An engine-resolved secret ref or an OIDC-minted key
	// wins; otherwise fall back to NUGET_API_KEY (and, if that is empty, to
	// dotnet's stored feed credentials — the pre-auth-seam behaviour).
	key, authNote, err := nugetAPIKey(ctx, req)
	if err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("dotnet nuget push: %w", err)
	}

	args := []string{"nuget", "push", nupkg, "--source", source, "--skip-duplicate"}
	if key != "" {
		args = append(args, "--api-key", key)
	}
	if _, _, err := runCmd(ctx, "", "dotnet", args...); err != nil {
		// dotnet nuget push takes the key on argv, so redact it from the error
		// (which echoes the command) before surfacing.
		return plugin.PublishResponse{}, fmt.Errorf("dotnet nuget push: %s", redact(err.Error(), key))
	}

	return plugin.PublishResponse{
		Published: true,
		Message:   fmt.Sprintf("pushed %s@%s to %s", req.Package.Name, req.Package.Version, source) + authNote,
	}, nil
}

// nugetAPIKey resolves the API key for a push and a short note describing how it
// was obtained. Precedence: an engine-resolved secret ref → an OIDC-minted key
// → NUGET_API_KEY → "" (let dotnet use stored feed credentials).
func nugetAPIKey(ctx context.Context, req plugin.PublishRequest) (key, note string, err error) {
	switch {
	case req.Auth != nil && req.Auth.Token != "":
		note = ""
		if req.Auth.Method == "secret-ref" {
			note = " (auth via secret reference)"
		}
		return req.Auth.Token, note, nil
	case req.OIDC:
		key, err = nugetOIDCKey(ctx, req.OIDCUser)
		if err != nil {
			return "", "", err
		}
		return key, " (auth via OIDC trusted publishing)", nil
	default:
		return os.Getenv("NUGET_API_KEY"), "", nil
	}
}

// redact replaces secret in text with a placeholder (no-op for empty secrets).
func redact(text, secret string) string {
	if strings.TrimSpace(secret) == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "***")
}

// Artifacts builds the NuGet package (`dotnet pack`) into req.OutputDir. The
// .nupkg is a registry artifact, so it is not attached to the GitHub release by
// default (Attach: false) — it ships to NuGet via Publish.
func (a *Adapter) Artifacts(ctx context.Context, req plugin.ArtifactsRequest) (plugin.ArtifactsResponse, error) {
	if req.Package.Private {
		return plugin.ArtifactsResponse{Skipped: true, Message: "private"}, nil
	}
	spec := req.Package.Name + "@" + req.Package.Version
	if req.DryRun {
		return plugin.ArtifactsResponse{Message: fmt.Sprintf("dry-run: would dotnet pack %s", spec)}, nil
	}
	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("dotnet pack: mkdir %s: %w", req.OutputDir, err)
	}
	manifest := filepath.Join(req.RepoRoot, req.Package.ManifestPath)
	if _, _, err := packRunner(ctx, req.RepoRoot, "dotnet", packArgs(manifest, req.OutputDir, req.Package.Version)...); err != nil {
		return plugin.ArtifactsResponse{}, fmt.Errorf("dotnet pack: %w", err)
	}
	// dotnet pack names the package <PackageId>.<version>.nupkg; PackageId is the
	// adapter's Name and the version is Version, so the path is deterministic.
	nupkg := filepath.Join(req.OutputDir, req.Package.Name+"."+req.Package.Version+".nupkg")
	return plugin.ArtifactsResponse{
		Built:     true,
		Artifacts: []plugin.Artifact{{Path: nupkg, Kind: plugin.ArtifactPackage, Attach: false}},
		Message:   "packed " + spec,
	}, nil
}

// ReleaseInit declares .NET's release prerequisites. With OIDC trusted
// publishing in play (the default), no NUGET_API_KEY is required — instead we
// point the operator at the one-time Trusted Publisher setup, which shiprig
// cannot do for them, and remind them OIDC needs a username. With OIDC off, it
// falls back to declaring NUGET_API_KEY. dotnet pack produces the .nupkg
// natively, so there is no build-config file to scaffold.
func (a *Adapter) ReleaseInit(ctx context.Context, req plugin.ReleaseInitRequest) (plugin.ReleaseInitResponse, error) {
	if req.OIDC {
		return plugin.ReleaseInitResponse{
			Notes: []string{
				"publishes to NuGet.org via OIDC trusted publishing — no NUGET_API_KEY needed",
				"one-time: create a Trusted Publishing policy (nuget.org → account → Trusted Publishing) and set `dotnet.user` to its creator's username",
				"CI must grant `id-token: write`",
				"to use a key instead, set dotnet.oidc=\"off\" and provide NUGET_API_KEY (or dotnet.auth)",
			},
		}, nil
	}
	return plugin.ReleaseInitResponse{
		Tokens: []plugin.TokenSpec{{
			EnvVar: "NUGET_API_KEY",
			For:    "dotnet nuget push",
			URL:    "https://www.nuget.org/account/apikeys",
		}},
		Notes: []string{"publishes to NuGet (set dotnet.oidc + dotnet.user to enable tokenless OIDC publishing)"},
	}, nil
}

// runCmd runs name+args (optionally in dir, "" for the current directory) and
// returns captured stdout/stderr. On a non-zero exit the error wraps whichever
// stream carried the diagnosis (see failureDetail).
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
		err = fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, cmderr.Detail(stdout, stderr))
	}
	return stdout, stderr, err
}

// packRunner runs `dotnet pack`; a variable so tests can see the arguments
// without a toolchain.
var packRunner = runCmd

// packArgs is the `dotnet pack` command line for a project, packing into
// outDir. The version is passed explicitly: the .nupkg is looked for under the
// name the version implies, and a project whose number is computed at build
// time (MinVer from git tags, a CI stamp) would otherwise pack under whatever
// it computed — a file that is not there. A global property on the command
// line is one no target can override, so the release's number wins even
// there; for a project stamped in its manifest it merely repeats the file.
func packArgs(manifest, outDir, version string) []string {
	args := []string{"pack", manifest, "-c", "Release", "-o", outDir}
	if version != "" {
		args = append(args, "-p:Version="+version)
	}
	return args
}

// resolvedVersion is where a project's version lives and its current value.
type resolvedVersion struct {
	version  string
	filePath string
	element  versionElement
	shared   bool
}

// resolveVersion reads the inline version from a csproj, then falls back to the
// nearest ancestor Directory.Build.props.
func resolveVersion(csprojPath, csprojText string) (resolvedVersion, bool) {
	if rv, ok := fromText(csprojText, csprojPath, false); ok {
		return rv, true
	}
	for _, props := range ancestorPropsFiles(csprojPath) {
		content, err := os.ReadFile(props)
		if err != nil {
			continue
		}
		if rv, ok := fromText(string(content), props, true); ok {
			return rv, true
		}
	}
	return resolvedVersion{}, false
}

// fromText extracts <Version> (preferred) or <VersionPrefix> from a document's text.
//
// Only elements inside a <PropertyGroup> count. MSBuild item metadata is written
// with the same element syntax, so a custom item can legitimately carry its own
// <Version> — Avalite's icon-pack items declare the pack's version that way:
//
//	<ItemGroup>
//	  <IconPack Include="lucide">
//	    <Version>0.2.0</Version>   <-- the icon pack, NOT the project
//
// A document-wide scan reported that as the project's version, and the matching
// write path would then have rewritten the pack's metadata on the next bump.
func fromText(text, filePath string, shared bool) (resolvedVersion, bool) {
	if m := findInPropertyGroup(text, versionRe); m != nil {
		if v := strings.TrimSpace(text[m[4]:m[5]]); v != "" {
			return resolvedVersion{version: v, filePath: filePath, element: elemVersion, shared: shared}, true
		}
	}
	if m := findInPropertyGroup(text, versionPrefixRe); m != nil {
		if v := strings.TrimSpace(text[m[4]:m[5]]); v != "" {
			return resolvedVersion{version: v, filePath: filePath, element: elemVersionPrefix, shared: shared}, true
		}
	}
	return resolvedVersion{}, false
}

// packable reports whether a project with no version in the tree still
// produces a package: it says so (`IsPackable` true, or a `PackageId`, which
// nobody declares for a project that never packs), or it hands its version to
// MinVer, in the project itself or in a Directory.Build.props above it. An
// explicit `IsPackable` false is the last word, whatever else is declared.
func packable(csprojPath, csprojText string) bool {
	texts := []string{csprojText}
	for _, props := range ancestorPropsFiles(csprojPath) {
		if content, err := os.ReadFile(props); err == nil {
			texts = append(texts, string(content))
		}
	}
	claimed := false
	for _, text := range texts {
		if m := findInPropertyGroup(text, isPackableRe); m != nil {
			switch strings.ToLower(strings.TrimSpace(text[m[2]:m[3]])) {
			case "false":
				return false
			case "true":
				claimed = true
			}
		}
		if packageID(text) != "" || minVerRe.MatchString(text) {
			claimed = true
		}
	}
	return claimed
}

// propertyGroupSpans returns the [start,end) byte range of the INNER text of each
// <PropertyGroup> block. PropertyGroups do not nest, so pairing each open tag with
// the next close tag is exact.
func propertyGroupSpans(text string) [][2]int {
	var spans [][2]int
	for _, open := range propertyGroupRe.FindAllStringIndex(text, -1) {
		// A self-closing <PropertyGroup ... /> contains nothing.
		if open[1] >= 2 && text[open[1]-2] == '/' {
			continue
		}
		rest := text[open[1]:]
		end := strings.Index(rest, "</PropertyGroup>")
		if end < 0 {
			// Unterminated (truncated or malformed file): treat the remainder as the
			// group rather than dropping properties that really are inside one.
			spans = append(spans, [2]int{open[1], len(text)})
			break
		}
		spans = append(spans, [2]int{open[1], open[1] + end})
	}
	return spans
}

// findInPropertyGroup returns FindStringSubmatchIndex for the first match of re
// lying inside a <PropertyGroup>, or nil when there is none. Read and write share
// it so they always target the same element.
func findInPropertyGroup(text string, re *regexp.Regexp) []int {
	spans := propertyGroupSpans(text)
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		for _, s := range spans {
			if m[0] >= s[0] && m[1] <= s[1] {
				return m
			}
		}
	}
	return nil
}

// ancestorPropsFiles yields existing Directory.Build.props files walking up from
// the csproj's directory, nearest first.
func ancestorPropsFiles(csprojPath string) []string {
	var out []string
	dir := filepath.Dir(csprojPath)
	for {
		candidate := filepath.Join(dir, propsFileName)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			out = append(out, candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// packageID returns the <PackageId> value, or "" when absent.
func packageID(text string) string {
	if m := packageIDRe.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// projectReferences extracts the intra-repo dependencies from <ProjectReference
// Include="..."/> elements. .NET project references carry no version range, so
// Range is empty (the cascade treats rangeless deps as always-patch-bump).
func projectReferences(text string) []plugin.Dependency {
	var deps []plugin.Dependency
	for _, m := range projectRefRe.FindAllStringSubmatch(text, -1) {
		include := strings.ReplaceAll(m[1], `\`, "/")
		name := strings.TrimSuffix(filepath.Base(include), ".csproj")
		if name == "" {
			continue
		}
		deps = append(deps, plugin.Dependency{Name: name, Kind: plugin.DepNormal})
	}
	return deps
}

// packageReferences extracts <PackageReference Include="..."/> names, marked as
// reached through the registry.
//
// Most of these are ordinary third-party packages and mean nothing to this repo.
// The caller keeps only the ones naming a package this repo also produces — a
// sibling consumed the way an outside consumer would — which is invisible in a
// ProjectReference-only view and is precisely what a local overlay redirects.
//
// The Include is the package identity, not a path, so unlike a ProjectReference
// there is no file name to strip and no chance of splitting it at a dot.
func packageReferences(text string) []plugin.Dependency {
	var deps []plugin.Dependency
	for _, m := range packageRefRe.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" {
			continue
		}
		deps = append(deps, plugin.Dependency{Name: name, Kind: plugin.DepNormal, ViaRegistry: true})
	}
	return deps
}

// writeVersion replaces the element value in place, or inserts the element into
// the first <PropertyGroup> when it is absent.
func writeVersion(text string, elem versionElement, newVersion string) (string, error) {
	tag := elem.tag()
	re := versionRe
	if elem == elemVersionPrefix {
		re = versionPrefixRe
	}
	// Replace only the FIRST matching element INSIDE A PROPERTYGROUP — the same one
	// fromText reads. Two reasons for each half:
	//   • First: a project can declare several <Version> elements under different
	//     <PropertyGroup Condition="..."> blocks; rewriting every one would collapse
	//     the per-condition versions into a single value.
	//   • PropertyGroup-scoped: <ItemGroup> item metadata uses the same element
	//     syntax, so an unscoped match could rewrite a custom item's <Version>
	//     (Avalite's icon packs declare one) and leave the project untouched.
	if loc := findInPropertyGroup(text, re); loc != nil {
		// Group 2 (the value) spans [loc[4], loc[5]); splice literally so the new
		// version is not subject to `$` expansion.
		return text[:loc[4]] + newVersion + text[loc[5]:], nil
	}

	// Absent (an independent override in a project that inherited its version):
	// add it to the first PropertyGroup.
	loc := propertyGroupRe.FindStringIndex(text)
	if loc == nil {
		// No PropertyGroup to attach to; leave the file untouched rather than
		// corrupting it.
		return text, nil
	}
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	addition := newline + "    <" + tag + ">" + newVersion + "</" + tag + ">"
	return text[:loc[1]] + addition + text[loc[1]:], nil
}

// relTo returns path relative to root, falling back to path on error.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
