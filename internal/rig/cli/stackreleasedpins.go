package cli

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type stackReleasePinFile struct {
	direct, central, centralEnabled bool
	versionless                     bool
	references                      []string
}

func stackReleasePinFileRead(path, pkg string) (stackReleasePinFile, error) {
	var result stackReleasePinFile
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	fail := func(reason string) (stackReleasePinFile, error) {
		return result, fmt.Errorf("%s: unsafe package version: %s; guards and pins unchanged", path, reason)
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	var conditions []bool
	var disabled []bool
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail("invalid XML")
		}
		if _, ok := tok.(xml.EndElement); ok {
			if len(conditions) > 0 {
				conditions = conditions[:len(conditions)-1]
				disabled = disabled[:len(disabled)-1]
			}
			continue
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := map[string]string{}
		for _, a := range el.Attr {
			key := strings.ToLower(a.Name.Local)
			if _, exists := attrs[key]; exists {
				return fail("duplicate MSBuild attribute")
			}
			attrs[key] = a.Value
		}
		conditional := len(conditions) > 0 && conditions[len(conditions)-1]
		if _, ok := attrs["condition"]; ok {
			conditional = true
		}
		conditions = append(conditions, conditional)
		inactive := len(disabled) > 0 && disabled[len(disabled)-1]
		condition := strings.ToLower(strings.TrimSpace(attrs["condition"]))
		if condition == "'$(usestacksources)' != 'false'" {
			inactive = true
		}
		disabled = append(disabled, inactive)
		if inactive {
			continue
		}
		name := strings.ToLower(el.Name.Local)
		if name == "import" || name == "importgroup" {
			return fail("explicit imports require MSBuild evaluation")
		}
		if name == "managepackageversionscentrally" {
			var value string
			if err := d.DecodeElement(&value, &el); err != nil {
				return fail("invalid central management setting")
			}
			conditions = conditions[:len(conditions)-1]
			disabled = disabled[:len(disabled)-1]
			if conditional || !strings.EqualFold(strings.TrimSpace(value), "true") {
				return fail("unproven central management setting")
			}
			result.centralEnabled = true
			continue
		}
		if name == "projectreference" {
			identity := attrs["include"]
			if conditional || identity == "" || stackReleaseDynamicIdentity(identity) || len(attrs) != 1 || !bytes.HasSuffix(bytes.TrimSpace(data[start:int(d.InputOffset())]), []byte("/>")) {
				return fail("unresolved project reference")
			}
			result.references = append(result.references, filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(strings.ReplaceAll(identity, "\\", "/")))))
			continue
		}
		if name != "packagereference" && name != "packageversion" {
			continue
		}
		if attrs["include"] == "" && attrs["update"] == "" && attrs["remove"] == "" {
			return fail("default package reference metadata")
		}
		match := false
		for _, key := range []string{"include", "update", "remove"} {
			identity, exists := attrs[key]
			if !exists {
				continue
			}
			if stackReleaseDynamicIdentity(identity) {
				return fail("unresolved package identity")
			}
			if strings.EqualFold(identity, pkg) {
				match = true
			}
		}
		if !match {
			continue
		}
		if _, ok := attrs["versionoverride"]; ok {
			return fail("VersionOverride on " + pkg)
		}
		if _, ok := attrs["update"]; ok {
			return fail("Update on " + pkg)
		}
		if _, ok := attrs["remove"]; ok {
			return fail("Remove on " + pkg)
		}
		if conditional {
			return fail("conditional pin for " + pkg)
		}
		if !bytes.HasSuffix(bytes.TrimSpace(data[start:int(d.InputOffset())]), []byte("/>")) {
			return fail("nested reference metadata for " + pkg)
		}
		version := attrs["version"]
		if name == "packagereference" {
			if !strings.EqualFold(filepath.Ext(path), ".csproj") {
				return fail("inherited PackageReference in build props/targets for " + pkg)
			}
			if version == "" {
				result.versionless = true
				continue
			}
			if _, ok := stackParseNugetVersion(version); !ok {
				return fail("unresolved or floating version for " + pkg)
			}
			result.direct = true
		} else {
			if !strings.EqualFold(filepath.Base(path), "Directory.Packages.props") {
				return fail("PackageVersion outside Directory.Packages.props")
			}
			if _, ok := stackParseNugetVersion(version); !ok {
				return fail("unresolved central version for " + pkg)
			}
			result.central = true
		}
	}
	return result, nil
}

func stackReleaseDynamicIdentity(s string) bool { return strings.ContainsAny(s, "$@%*?;") }

// Without evaluating MSBuild, only a deliberately small package-resolution
// subset is provable. Inspect automatic ancestor files too: a local central pin
// alone says nothing about a later Directory.Build.targets override.
func stackReleaseProvePins(root, pkg string) error { return stackReleaseCheckPins(root, pkg, true) }

func stackReleaseMigrationEdits(root, guardedPackage, pin, version, symbol string, warnings ...string) ([]stackReleaseEdit, []string, error) {
	if !strings.EqualFold(guardedPackage, pin) {
		if err := stackReleaseCheckPins(root, guardedPackage, false); err != nil {
			return nil, nil, err
		}
	}
	return stackReleaseEdits(root, pin, version, symbol, warnings...)
}

func stackReleaseCheckPins(root, pkg string, requireDelivery bool) error {
	files := map[string]stackReleasePinFile{}
	var projects []string
	inspect := func(path string) error {
		if _, ok := files[path]; ok {
			return nil
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: unsafe package version: nonregular build file", path)
		}
		f, err := stackReleasePinFileRead(path, pkg)
		if err != nil {
			return err
		}
		files[path] = f
		return nil
	}
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			switch strings.ToLower(e.Name()) {
			case ".git", "bin", "obj", "vendor", "node_modules", "packages", "generated", ".vs":
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".csproj" && ext != ".props" && ext != ".targets" {
			return nil
		}
		if err := inspect(path); err != nil {
			return err
		}
		if ext == ".csproj" {
			projects = append(projects, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for dir := filepath.Dir(root); ; dir = filepath.Dir(dir) {
		for _, name := range []string{"Directory.Build.props", "Directory.Build.targets", "Directory.Packages.props"} {
			if err := inspect(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if !requireDelivery {
		for path, f := range files {
			if f.direct || f.versionless || f.central {
				return fmt.Errorf("%s: unsafe package version: explicit sibling pin may override transitive delivery of %s; guards and pins unchanged", path, pkg)
			}
		}
		return nil
	}
	if len(projects) == 0 {
		return fmt.Errorf("unsafe package version: no projects to prove")
	}
	for _, project := range projects {
		f := files[project]
		if !f.versionless {
			continue
		}
		enabled := f.centralEnabled
		proved := false
		for dir := filepath.Dir(project); ; dir = filepath.Dir(dir) {
			// Directory.Build.props/targets may enable CPM, but cannot introduce a
			// selected reference or override it in this supported subset.
			for _, name := range []string{"Directory.Build.props", "Directory.Build.targets"} {
				enabled = enabled || files[filepath.Join(dir, name)].centralEnabled
			}
			central, exists := files[filepath.Join(dir, "Directory.Packages.props")]
			if exists {
				proved = central.central
				enabled = enabled || central.centralEnabled
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
		if !proved || !enabled {
			return fmt.Errorf("%s: unsafe package version: versionless reference has no proven effective central pin; guards and pins unchanged", project)
		}
		// Central files outside the member are inspected but not edited.
		centralInside := false
		for dir := filepath.Dir(project); ; dir = filepath.Dir(dir) {
			if _, ok := files[filepath.Join(dir, "Directory.Packages.props")]; ok {
				rel, _ := filepath.Rel(root, dir)
				centralInside = rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
		if !centralInside {
			return fmt.Errorf("%s: unsafe package version: effective central pin is outside the member", project)
		}
		f.direct = true
		files[project] = f
	}
	var delivered func(string, map[string]bool) bool
	delivered = func(project string, seen map[string]bool) bool {
		if seen[project] {
			return false
		}
		seen[project] = true
		f, ok := files[project]
		if !ok {
			return false
		}
		if f.direct {
			return true
		}
		for _, reference := range f.references {
			if delivered(reference, seen) {
				return true
			}
		}
		return false
	}
	for _, project := range projects {
		if !delivered(project, map[string]bool{}) {
			return fmt.Errorf("%s: unsafe package version: cannot prove this project receives %s through a literal pin or local project reference; guards and pins unchanged", project, pkg)
		}
	}
	return nil
}
