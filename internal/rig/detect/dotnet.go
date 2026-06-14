// .NET project discovery, ported from the .NET rig's ProjectDiscovery:
// convention-first, no MSBuild evaluation. Locates a solution (config override
// → first *.slnx → first *.sln) and reads each project's OutputType, target
// framework, and test signals straight from the csproj. When no solution
// exists, falls back to scanning *.csproj under the root (skipping bin/obj).
package detect

import (
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// DiscoverDotNet lists the .NET projects under root, from the solution when one
// exists (configuredSolution override → first *.slnx → first *.sln), otherwise
// by scanning for *.csproj. Projects matching an exclude glob are dropped.
// The result is sorted by name (case-insensitive).
func DiscoverDotNet(root, configuredSolution string, exclude []string) []ProjectInfo {
	var csprojs []string
	if solution := FindSolution(root, configuredSolution); solution != "" {
		csprojs = SolutionProjects(solution)
	} else {
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

// SolutionProjects returns the absolute csproj paths referenced by a solution
// (*.slnx XML or classic *.sln), deduped, non-csproj entries dropped.
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
		if !hasSuffixFold(rel, ".csproj") {
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
	}
}

type csprojProps struct {
	outputType, tfm, tfms, assemblyName string
	isTestProject, enableMSTest         string
	refsTestSdk                         bool
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
		}
	}
}

// scanForProjects finds every *.csproj under root, skipping bin/obj output
// directories. Used only when no solution exists.
func scanForProjects(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree — skip, discovery is best-effort
		}
		if d.IsDir() {
			if name := d.Name(); strings.EqualFold(name, "bin") || strings.EqualFold(name, "obj") {
				return filepath.SkipDir
			}
			return nil
		}
		if hasSuffixFold(d.Name(), ".csproj") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

func isTrue(value string) bool { return strings.EqualFold(strings.TrimSpace(value), "true") }

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
