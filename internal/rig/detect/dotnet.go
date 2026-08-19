// .NET project discovery, ported from the .NET rig's ProjectDiscovery:
// convention-first, no MSBuild evaluation. Scans for project files under the
// root (skipping build output), or reads the solution named by .rig.json's
// `solution` when one is pinned, then reads each project's OutputType, target
// framework, and test signals straight from the project file.
package detect

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dotnetadapter "github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
)

// ProjectInfo is a project discovered from the solution (or a csproj scan).
type ProjectInfo struct {
	Name     string
	RelPath  string
	FullPath string
	// OutputType is the csproj <OutputType> ("" when absent → library).
	OutputType string
	// Tfm is the (first) target framework, e.g. "net9.0".
	Tfm string
	// AssemblyName is the csproj <AssemblyName> when set and literal.
	AssemblyName string
	IsTest       bool
	// Deps are the names of intra-repo projects this one references (the base
	// name of each <ProjectReference Include="…"/>), for dependency ordering.
	Deps []string
}

// OutputName is the process/output name (AssemblyName when set, else Name).
func (p ProjectInfo) OutputName() string {
	if p.AssemblyName != "" {
		return p.AssemblyName
	}
	return p.Name
}

// ShortName is the last dotted segment of the project name.
func (p ProjectInfo) ShortName() string {
	if i := strings.LastIndex(p.Name, "."); i >= 0 {
		return p.Name[i+1:]
	}
	return p.Name
}

// IsRunnable reports whether the project produces a runnable executable.
func (p ProjectInfo) IsRunnable() bool {
	return !p.IsTest &&
		(strings.EqualFold(p.OutputType, "Exe") || strings.EqualFold(p.OutputType, "WinExe"))
}

// DiscoverDotNet lists the .NET projects under root: the projects of the
// CONFIGURED solution when .rig.json pins one, otherwise every project file
// found by scanning. Projects matching an exclude glob are dropped. The result
// is sorted by name (case-insensitive).
//
// Discovery deliberately does NOT auto-pick a solution. A repo can hold several
// (per-package solutions under subdirectories, a test-only aggregate at the
// root, an IDE-convenience solution covering a slice), and picking one of them
// — the old behavior took the first root-level *.slnx — silently hid every
// project outside it: `rig info` listed a fraction of the repo and `rig run
// <app>` couldn't see the app. Which solution "the" solution is, is a question
// with no convention-first answer, so rig stops guessing and reports what is
// actually on disk. A `solution` in .rig.json is an explicit answer and still
// scopes discovery to that file; `exclude` trims whatever the scan over-reports.
func DiscoverDotNet(root, configuredSolution string, exclude []string) []ProjectInfo {
	var csprojs []string
	// Branch on whether the pinned solution was FOUND, not on whether it yielded
	// projects. A solution that exists but lists none — empty, or all of them
	// unsupported, or unparseable — returns a nil slice, and treating that as
	// "no pin" would silently widen an explicitly scoped repo to a full scan:
	// the one outcome a pin exists to prevent.
	//
	// A pin naming a MISSING file still falls through to the scan rather than
	// reporting an empty repo; `rig doctor` flags that separately.
	pinned := false
	if configuredSolution != "" {
		if solution := FindSolution(root, configuredSolution); solution != "" {
			csprojs = SolutionProjects(solution)
			pinned = true
		}
	}
	if !pinned {
		csprojs = scanForProjects(root)
	}

	var projects []ProjectInfo
	for _, path := range csprojs {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		p := LoadProject(path, root)
		if !IsExcluded(p, exclude) {
			projects = append(projects, p)
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	return projects
}

// IsExcluded reports whether the project matches an `exclude` glob by its name
// or its (forward-slashed) relative path — so both "*Bench" and "samples/*"
// work.
func IsExcluded(project ProjectInfo, exclude []string) bool {
	rel := strings.ReplaceAll(project.RelPath, "\\", "/")
	for _, pattern := range exclude {
		if GlobMatch(pattern, project.Name) || GlobMatch(pattern, rel) {
			return true
		}
	}
	return false
}

// SolutionCandidates lists the solution file names at the root, *.slnx
// preferred (matching FindSolution's precedence).
func SolutionCandidates(root string) []string {
	var names []string
	for _, pat := range []string{"*.slnx", "*.sln"} {
		matches, _ := filepath.Glob(filepath.Join(root, pat))
		for _, m := range matches {
			names = append(names, filepath.Base(m))
		}
	}
	return names
}

// FindSolution locates the solution file for root: the configured override when
// set (must exist; "" otherwise), else the first *.slnx, else the first *.sln.
//
// The unconfigured fallback answers "does this repo have a solution at all"
// (Capabilities.HasSolution) and seeds the `rig init` wizard's suggestion. It is
// NOT what discovery lists projects from — see DiscoverDotNet for why the first
// root-level solution is a poor stand-in for the workspace.
func FindSolution(root, configuredSolution string) string {
	if configuredSolution != "" {
		full := configuredSolution
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, full)
		}
		if _, err := os.Stat(full); err == nil {
			return full
		}
		return ""
	}
	for _, pat := range []string{"*.slnx", "*.sln"} {
		if matches, _ := filepath.Glob(filepath.Join(root, pat)); len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// SolutionProjects returns the absolute project paths referenced by a solution
// (*.slnx XML or classic *.sln), deduped, non-project entries dropped.
func SolutionProjects(solutionPath string) []string {
	dir := filepath.Dir(solutionPath)
	var rels []string
	if strings.EqualFold(filepath.Ext(solutionPath), ".slnx") {
		rels = parseSlnx(solutionPath)
	} else {
		rels = parseSln(solutionPath)
	}

	var out []string
	seen := map[string]bool{}
	for _, rel := range rels {
		if !isProjectFile(rel) {
			continue
		}
		full := filepath.Clean(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(rel, "\\", "/"))))
		if !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	return out
}

// parseSlnx pulls every <Project Path="…"/> out of an XML solution, at any
// nesting depth (projects can live under <Folder> elements).
func parseSlnx(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rels []string
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err != nil {
			return rels // io.EOF or malformed XML — return what we have
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Project" {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Local == "Path" && a.Value != "" {
				rels = append(rels, a.Value)
			}
		}
	}
}

// slnProjectLine matches classic .sln entries of the form
//
//	Project("{TYPE-GUID}") = "Name", "relative\path.csproj", "{PROJECT-GUID}"
var slnProjectLine = regexp.MustCompile(`(?m)^Project\("\{[^}]+\}"\)\s*=\s*"[^"]*"\s*,\s*"([^"]+)"`)

func parseSln(path string) []string {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rels []string
	for _, m := range slnProjectLine.FindAllStringSubmatch(string(text), -1) {
		rels = append(rels, m[1])
	}
	return rels
}

// LoadProject reads a project's classification straight from its csproj:
// OutputType, target framework, AssemblyName, and the test signals
// (IsTestProject / EnableMSTestRunner / a Microsoft.NET.Test.Sdk reference /
// the *Tests naming convention). An unparseable csproj classifies as a
// non-test library with unknown TFM.
func LoadProject(csprojFullPath, root string) ProjectInfo {
	name := strings.TrimSuffix(filepath.Base(csprojFullPath), filepath.Ext(csprojFullPath))
	rel, err := filepath.Rel(root, csprojFullPath)
	if err != nil {
		rel = csprojFullPath
	}

	props := readCsproj(csprojFullPath)
	assemblyName := props.assemblyName
	if strings.Contains(assemblyName, "$") {
		assemblyName = "" // unevaluated MSBuild prop
	}
	tfm := props.tfm
	if tfm == "" && props.tfms != "" {
		for _, part := range strings.Split(props.tfms, ";") {
			if p := strings.TrimSpace(part); p != "" {
				tfm = p
				break
			}
		}
	}

	isTest := isTrue(props.isTestProject) || isTrue(props.enableMSTest) || props.refsTestSdk ||
		hasSuffixFold(name, "Tests")

	return ProjectInfo{
		Name:         name,
		RelPath:      rel,
		FullPath:     csprojFullPath,
		OutputType:   props.outputType,
		Tfm:          tfm,
		AssemblyName: assemblyName,
		IsTest:       isTest,
		Deps:         projectRefNames(props.projectRefs),
	}
}

// projectRefNames maps each <ProjectReference Include="…"/> path to the
// referenced project's name (its csproj base name without extension), the
// identity DiscoverDotNet uses for ProjectInfo.Name — so dependency edges line
// up by name. Backslashes are normalized so Windows-style paths resolve too.
func projectRefNames(includes []string) []string {
	if len(includes) == 0 {
		return nil
	}
	var deps []string
	for _, inc := range includes {
		base := filepath.Base(strings.ReplaceAll(inc, `\`, "/"))
		if name := strings.TrimSuffix(base, filepath.Ext(base)); name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}

type csprojProps struct {
	outputType, tfm, tfms, assemblyName string
	isTestProject, enableMSTest         string
	refsTestSdk                         bool
	projectRefs                         []string // <ProjectReference Include="…"/>
}

// readCsproj walks the csproj XML, recording the first non-empty value of each
// interesting property (at any depth) and whether Microsoft.NET.Test.Sdk is
// referenced. Parse errors abandon the walk, keeping whatever was read.
func readCsproj(path string) csprojProps {
	var props csprojProps
	f, err := os.Open(path)
	if err != nil {
		return props
	}
	defer f.Close()

	set := func(dst *string, dec *xml.Decoder, se xml.StartElement) {
		var v string
		if err := dec.DecodeElement(&v, &se); err == nil && *dst == "" && strings.TrimSpace(v) != "" {
			*dst = strings.TrimSpace(v)
		}
	}

	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err != nil {
			return props
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "OutputType":
			set(&props.outputType, dec, se)
		case "TargetFramework":
			set(&props.tfm, dec, se)
		case "TargetFrameworks":
			set(&props.tfms, dec, se)
		case "AssemblyName":
			set(&props.assemblyName, dec, se)
		case "IsTestProject":
			set(&props.isTestProject, dec, se)
		case "EnableMSTestRunner":
			set(&props.enableMSTest, dec, se)
		case "PackageReference":
			for _, a := range se.Attr {
				if a.Name.Local == "Include" && strings.EqualFold(a.Value, "Microsoft.NET.Test.Sdk") {
					props.refsTestSdk = true
				}
			}
		case "ProjectReference":
			for _, a := range se.Attr {
				if a.Name.Local == "Include" && a.Value != "" {
					props.projectRefs = append(props.projectRefs, a.Value)
				}
			}
		}
	}
}

// ProjectFileExts are the MSBuild project files a .NET project is found by.
// Defined in the adapter, which also gates whether .NET discovery runs at all —
// the two must agree or a repo is silently reported as having no projects.
var ProjectFileExts = dotnetadapter.ProjectFileExts

// scanSkipDirs are never descended into: build output and dependency trees,
// which hold copies of project files. `vendor` is deliberately absent — a
// vendored *.csproj is a first-class build input in .NET (solutions routinely
// list them), unlike a Go or PHP vendor tree; `exclude` hides one that isn't.
var scanSkipDirs = dotnetadapter.SkippedScanDirs

// scanForProjects finds every project file under root. This is the default
// discovery path (see DiscoverDotNet), so it is the superset a solution would
// otherwise carve up — anything unwanted is trimmed by the `exclude` globs.
func scanForProjects(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree — skip, discovery is best-effort
		}
		if d.IsDir() {
			for _, skip := range scanSkipDirs {
				if strings.EqualFold(d.Name(), skip) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if isProjectFile(d.Name()) {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

// isProjectFile reports whether name is an MSBuild project file.
func isProjectFile(name string) bool { return dotnetadapter.IsProjectFile(name) }

func isTrue(value string) bool { return strings.EqualFold(strings.TrimSpace(value), "true") }

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
