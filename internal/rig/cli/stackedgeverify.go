package cli

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// These expectations are data, so the publish job can verify downloaded
// artifacts without evaluating MSBuild or executing any project code.
type stackEdgeExpectedPackage struct {
	OriginalID     string
	ID             string
	AssemblyBefore string
	AssemblyAfter  string
	Dependencies   []stackEdgeDependency
}

type stackEdgeDependency struct {
	ID        string `xml:"id,attr"`
	Version   string `xml:"version,attr"`
	Framework string `xml:"-"`
}

type stackEdgeVerifyRequest struct {
	Version        string
	Packages       []stackEdgeExpectedPackage
	MemberIDs      map[string]string // all originals, including exclusions; empty alias means excluded
	OtherMemberIDs []string          // both original and republished ids
	MaxSize        uint64
}

type stackEdgeVerifiedPackage struct {
	File string
	ID   string
	Size uint64
}

type stackEdgeNuspec struct {
	ID           string `xml:"id"`
	Version      string `xml:"version"`
	Description  string `xml:"description"`
	Dependencies struct {
		Direct []stackEdgeDependency `xml:"dependency"`
		Groups []struct {
			Framework    string                `xml:"targetFramework,attr"`
			Dependencies []stackEdgeDependency `xml:"dependency"`
		} `xml:"group"`
	} `xml:"dependencies"`
}

// stackEdgeVerify verifies every nupkg in dir. The pack caller supplies a fresh
// staging directory containing only observed output; publish can supply its
// downloaded artifact directory. Adapter predictions never enter this seam.
func stackEdgeVerify(dir string, want stackEdgeVerifyRequest) ([]stackEdgeVerifiedPackage, error) {
	var reports []stackEdgeVerifiedPackage
	for _, file := range stackPackWalk(dir) {
		if !strings.HasSuffix(strings.ToLower(file), ".nupkg") {
			continue
		}
		stat, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file)))
		if err != nil {
			return reports, err
		}
		reports = append(reports, stackEdgeVerifiedPackage{File: file, Size: uint64(stat.Size())})
	}
	slices.SortFunc(reports, func(a, b stackEdgeVerifiedPackage) int { return strings.Compare(a.File, b.File) })
	expected := map[string]stackEdgeExpectedPackage{}
	member := map[string]bool{}
	for original, alias := range want.MemberIDs {
		member[strings.ToLower(original)] = true
		if alias != "" {
			member[strings.ToLower(alias)] = true
		}
	}
	for _, pkg := range want.Packages {
		key := strings.ToLower(pkg.ID)
		if key == "" || strings.EqualFold(pkg.ID, pkg.OriginalID) || expected[key].ID != "" {
			return reports, fmt.Errorf("invalid or duplicate republished id %q", pkg.ID)
		}
		expected[key] = pkg
		member[strings.ToLower(pkg.OriginalID)], member[key] = true, true
	}
	other := map[string]bool{}
	for _, id := range want.OtherMemberIDs {
		other[strings.ToLower(id)] = true
	}
	seen := map[string]bool{}
	for i := range reports {
		report := &reports[i]
		spec, assemblies, err := stackEdgeReadNupkg(filepath.Join(dir, filepath.FromSlash(report.File)))
		if err != nil {
			return reports, fmt.Errorf("%s: %w", report.File, err)
		}
		report.ID = spec.ID
		key := strings.ToLower(spec.ID)
		pkg, ok := expected[key]
		if !ok { // check 1: fail closed even for another well-formed NuGet id
			return reports, fmt.Errorf("%s: id %q is not a republished, non-excluded package", report.File, spec.ID)
		}
		if seen[key] {
			return reports, fmt.Errorf("duplicate output for %s", spec.ID)
		}
		seen[key] = true
		if !stackEdgeSameVersion(spec.Version, want.Version) {
			return reports, fmt.Errorf("%s: version %s differs from edge version %s", spec.ID, spec.Version, want.Version)
		}
		deps := append([]stackEdgeDependency{}, spec.Dependencies.Direct...)
		for _, group := range spec.Dependencies.Groups {
			for _, dep := range group.Dependencies {
				dep.Framework = group.Framework
				deps = append(deps, dep)
			}
		}
		observed := map[string]bool{}
		for _, dep := range deps {
			dkey := strings.ToLower(dep.ID)
			if other[dkey] {
				return reports, fmt.Errorf("%s depends on another member's package %s", spec.ID, dep.ID)
			}
			if member[dkey] {
				if expected[dkey].ID == "" || !stackEdgeDependencyVersion(dep.Version, want.Version) { // check 2
					return reports, fmt.Errorf("%s: member dependency %s@%s must name a non-excluded republished id at %s", spec.ID, dep.ID, dep.Version, want.Version)
				}
			}
			match := false
			for _, original := range pkg.Dependencies {
				if strings.EqualFold(original.ID, dep.ID) && stackEdgeRange(original.Version) == stackEdgeRange(dep.Version) && (original.Framework == "" || stackEdgeSameFramework(original.Framework, dep.Framework)) {
					match = true
					observed[stackEdgeDependencyKey(original)] = true
					break
				}
			}
			if !match {
				return reports, fmt.Errorf("%s: dependency %s@%s differs from discovery", spec.ID, dep.ID, dep.Version)
			}
		}
		for _, dep := range pkg.Dependencies {
			if !observed[stackEdgeDependencyKey(dep)] {
				return reports, fmt.Errorf("%s: dependency %s@%s from discovery is missing", spec.ID, dep.ID, dep.Version)
			}
		}
		if pkg.AssemblyBefore == "" || pkg.AssemblyBefore != pkg.AssemblyAfter { // check 5
			return reports, fmt.Errorf("%s: AssemblyName changed from %q to %q", spec.ID, pkg.AssemblyBefore, pkg.AssemblyAfter)
		}
		if len(assemblies) > 0 && !slices.Contains(assemblies, pkg.AssemblyBefore+".dll") {
			return reports, fmt.Errorf("%s: expected assembly %s.dll is missing", spec.ID, pkg.AssemblyBefore)
		}
		if report.Size > want.MaxSize {
			return reports, fmt.Errorf("%s: size %d bytes exceeds maxSize %d bytes", spec.ID, report.Size, want.MaxSize)
		}
	}
	for key, pkg := range expected {
		if !seen[key] {
			return reports, fmt.Errorf("no observed nupkg for %s", pkg.ID)
		}
	}
	if len(expected) == 0 {
		return reports, fmt.Errorf("no non-excluded packages to verify")
	}
	return reports, nil
}

func stackEdgeReadNupkg(path string) (stackEdgeNuspec, []string, error) {
	var spec stackEdgeNuspec
	archive, err := zip.OpenReader(path)
	if err != nil {
		return spec, nil, err
	}
	defer archive.Close()
	var assemblies []string
	count := 0
	for _, file := range archive.File {
		if (strings.HasPrefix(file.Name, "lib/") || strings.HasPrefix(file.Name, "ref/")) && strings.HasSuffix(file.Name, ".dll") {
			assemblies = append(assemblies, filepath.Base(file.Name))
		}
		if !strings.HasSuffix(strings.ToLower(file.Name), ".nuspec") {
			continue
		}
		count++
		stream, err := file.Open()
		if err != nil {
			return spec, nil, err
		}
		var envelope struct {
			Metadata stackEdgeNuspec `xml:"metadata"`
		}
		err = xml.NewDecoder(io.LimitReader(stream, 2<<20)).Decode(&envelope)
		stream.Close()
		if err != nil {
			return spec, nil, err
		}
		spec = envelope.Metadata
	}
	if count != 1 || spec.ID == "" || spec.Version == "" {
		return spec, nil, fmt.Errorf("expected exactly one complete nuspec")
	}
	return spec, assemblies, nil
}

func stackEdgeSameVersion(a, b string) bool {
	x, ok := stackParseNugetVersion(a)
	y, valid := stackParseNugetVersion(b)
	return ok && valid && stackCompareNuget(x, y) == 0
}

func stackEdgeDependencyVersion(got, version string) bool {
	return stackEdgeRange(got) == stackEdgeRange(version) || strings.HasPrefix(got, "[") && strings.HasSuffix(got, "]") && stackEdgeSameVersion(got[1:len(got)-1], version)
}

func stackEdgeDependencyKey(dep stackEdgeDependency) string {
	return strings.ToLower(dep.ID) + "@" + stackEdgeRange(dep.Version) + "/" + stackEdgeFramework(dep.Framework)
}

// The SDK can fill an omitted platform version when writing the nuspec. Treat
// that as the same framework, but never erase a difference between two explicit
// platform versions or between different platforms or target frameworks.
func stackEdgeSameFramework(a, b string) bool {
	a, b = stackEdgeFramework(a), stackEdgeFramework(b)
	if a == b {
		return true
	}
	baseA, platformA, hasPlatformA := strings.Cut(a, "-")
	baseB, platformB, hasPlatformB := strings.Cut(b, "-")
	if !hasPlatformA || !hasPlatformB || baseA != baseB {
		return false
	}
	split := func(platform string) (string, string) {
		i := strings.IndexFunc(platform, func(r rune) bool { return r >= '0' && r <= '9' })
		if i < 0 {
			return platform, ""
		}
		return platform[:i], platform[i:]
	}
	nameA, versionA := split(platformA)
	nameB, versionB := split(platformB)
	return nameA != "" && nameA == nameB && (versionA == "" || versionB == "" || versionA == versionB)
}

// NuGet accepts both the long framework name and SDK short forms in nuspecs.
func stackEdgeFramework(raw string) string {
	value := strings.ToLower(raw)
	if strings.HasPrefix(value, ".netstandard") {
		return strings.TrimPrefix(value, ".")
	}
	if strings.HasPrefix(value, ".netframework") {
		return "net" + strings.ReplaceAll(strings.TrimPrefix(value, ".netframework"), ".", "")
	}
	if strings.HasPrefix(value, ".netcoreapp") {
		version := strings.TrimPrefix(value, ".netcoreapp")
		major, _ := strconv.Atoi(strings.Split(version, ".")[0])
		if major >= 5 {
			return "net" + version
		}
		return "netcoreapp" + version
	}
	return value
}

// NuGet writes normalized bounds and treats a bare version as a lower bound.
// Preserve exact pins ([v]) and upper bounds; never loosen them for comparison.
func stackEdgeRange(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	normalize := func(v string) string {
		if parsed, ok := stackParseNugetVersion(v); ok {
			return parsed.normalized()
		}
		return strings.ToLower(v)
	}
	if len(raw) > 1 && (raw[0] == '[' || raw[0] == '(') {
		parts := strings.Split(raw[1:len(raw)-1], ",")
		for i := range parts {
			parts[i] = normalize(parts[i])
		}
		if len(parts) == 2 && parts[1] == "" && raw[0] == '[' && raw[len(raw)-1] == ')' {
			return parts[0]
		}
		return raw[:1] + strings.Join(parts, ",") + raw[len(raw)-1:]
	}
	return normalize(raw)
}
