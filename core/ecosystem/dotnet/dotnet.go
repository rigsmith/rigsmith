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

	"github.com/rigsmith/core/plugin"
	"github.com/rigsmith/core/walkutil"
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
	projectRefRe    = regexp.MustCompile(`<ProjectReference[^>]*\bInclude\s*=\s*"([^"]*)"`)
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
		Capabilities:     []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish},
		ManifestPatterns: []string{"*.csproj"},
		DevCommands: map[string][]string{
			plugin.VerbBuild:     {"dotnet", "build"},
			plugin.VerbTest:      {"dotnet", "test"},
			plugin.VerbRun:       {"dotnet", "run"},
			plugin.VerbFormat:    {"dotnet", "format"},
			plugin.VerbCoverage:  {"dotnet", "test", "--collect:XPlat Code Coverage"},
			plugin.VerbInstall:   {"dotnet", "restore"},
			plugin.VerbAdd:       {"dotnet", "add", "package"},
			plugin.VerbUninstall: {"dotnet", "remove", "package"},
			plugin.VerbOutdated:  {"dotnet", "list", "package", "--outdated"},
			plugin.VerbClean:     {"dotnet", "clean"},
			plugin.VerbGlobal:    {"dotnet", "tool", "install", "--global"},
			plugin.VerbDlx:       {"dnx"},
		},
	}
}

// Detect reports whether any .csproj exists under root.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	found := false
	err := walkutil.Walk(root, func(path string, d fs.DirEntry) error {
		if strings.HasSuffix(path, ".csproj") {
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
		// project with no version anywhere is skipped (matches the C# original).
		resolved, ok := resolveVersion(path, text)
		if !ok {
			return nil
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
	elem := elemVersion
	if !strings.Contains(text, "<Version>") && strings.Contains(text, "<VersionPrefix>") {
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
	if _, _, err := runCmd(ctx, "", "dotnet", "pack", manifest, "-c", "Release", "-o", tmpDir); err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("dotnet pack: %w", err)
	}

	// The PackageId is req.Package.Name and the version is req.Package.Version, so
	// the produced artifact is deterministically named.
	nupkg := filepath.Join(tmpDir, req.Package.Name+"."+req.Package.Version+".nupkg")

	args := []string{"nuget", "push", nupkg, "--source", source, "--skip-duplicate"}
	if key := os.Getenv("NUGET_API_KEY"); key != "" {
		args = append(args, "--api-key", key)
	}
	if _, _, err := runCmd(ctx, "", "dotnet", args...); err != nil {
		return plugin.PublishResponse{}, fmt.Errorf("dotnet nuget push: %w", err)
	}

	return plugin.PublishResponse{
		Published: true,
		Message:   fmt.Sprintf("pushed %s@%s to %s", req.Package.Name, req.Package.Version, source),
	}, nil
}

// runCmd runs name+args (optionally in dir, "" for the current directory) and
// returns captured stdout/stderr. On a non-zero exit the error wraps stderr for
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
func fromText(text, filePath string, shared bool) (resolvedVersion, bool) {
	if m := versionRe.FindStringSubmatch(text); m != nil {
		if v := strings.TrimSpace(m[2]); v != "" {
			return resolvedVersion{version: v, filePath: filePath, element: elemVersion, shared: shared}, true
		}
	}
	if m := versionPrefixRe.FindStringSubmatch(text); m != nil {
		if v := strings.TrimSpace(m[2]); v != "" {
			return resolvedVersion{version: v, filePath: filePath, element: elemVersionPrefix, shared: shared}, true
		}
	}
	return resolvedVersion{}, false
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

// writeVersion replaces the element value in place, or inserts the element into
// the first <PropertyGroup> when it is absent.
func writeVersion(text string, elem versionElement, newVersion string) (string, error) {
	tag := elem.tag()
	if strings.Contains(text, "<"+tag+">") {
		re := versionRe
		if elem == elemVersionPrefix {
			re = versionPrefixRe
		}
		return re.ReplaceAllString(text, "${1}"+newVersion+"${3}"), nil
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
