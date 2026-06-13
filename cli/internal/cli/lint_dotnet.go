package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// `rig lint` for .NET runs `dotnet format analyzers --verify-no-changes`, which
// surfaces whatever Roslyn analyzers the projects reference. Modern SDKs enable
// the built-in CA analyzers by default, but the high-signal rule sets come from
// third-party analyzer packages (SonarAnalyzer.CSharp, Meziantou.Analyzer,
// StyleCop.Analyzers, Roslynator, …). doctor reports which, if any, are wired up
// so `rig lint` doesn't quietly run on nothing.

// pkgRefRe extracts the package id from a <PackageReference Include="…"> or a
// <PackageVersion Include="…"> (central package management) element.
var pkgRefRe = regexp.MustCompile(`(?i)<Package(?:Reference|Version)\b[^>]*\bInclude\s*=\s*"([^"]+)"`)

// dotnetAnalyzerPackages returns the sorted, de-duplicated analyzer package ids
// referenced anywhere under root — across project files and the central
// Directory.Build.props / Directory.Packages.props. A package counts as an
// analyzer when its id contains "analyzer" (case-insensitive), which covers the
// common rule sets without hard-coding a brittle allowlist.
func dotnetAnalyzerPackages(root string) []string {
	skip := map[string]bool{"bin": true, "obj": true, ".git": true, "node_modules": true, "vendor": true}
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDotnetAnalyzerSource(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range pkgRefRe.FindAllStringSubmatch(string(data), -1) {
			id := strings.TrimSpace(m[1])
			if strings.Contains(strings.ToLower(id), "analyzer") {
				seen[id] = true
			}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// isDotnetAnalyzerSource reports whether a file can carry analyzer package
// references: a project file or a central MSBuild props file.
func isDotnetAnalyzerSource(path string) bool {
	switch filepath.Ext(path) {
	case ".csproj", ".fsproj", ".vbproj":
		return true
	}
	switch filepath.Base(path) {
	case "Directory.Build.props", "Directory.Packages.props":
		return true
	}
	return false
}
