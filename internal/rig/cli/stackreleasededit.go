package cli

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type stackReleaseEdit struct {
	path          string
	before, after []byte
	mode          fs.FileMode
}

// Conservative lexical fence: files with strings or comments need a C# lexer.
// Refuse the entire file rather than accidentally treating their contents as directives.
func stackReleaseRewrite(data []byte, symbol string) ([]byte, string) {
	if !bytes.Contains(data, []byte(symbol)) {
		return data, ""
	}
	if !utf8.Valid(data) || bytes.ContainsAny(data, "\"'`") || bytes.Contains(data, []byte("//")) || bytes.Contains(data, []byte("/*")) {
		return data, "strings, comments or unsupported encoding; review manually"
	}
	bom := []byte{}
	if bytes.HasPrefix(data, []byte{239, 187, 191}) {
		bom = data[:3]
		data = data[3:]
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	type frame struct{ target, drop, otherwise bool }
	var frames []frame
	var out bytes.Buffer
	out.Write(bom)
	active := func() bool {
		for _, f := range frames {
			if f.drop {
				return false
			}
		}
		return true
	}
	refuse := func(reason string) ([]byte, string) { return append(append([]byte{}, bom...), data...), reason }
	for _, line := range lines {
		s := strings.TrimSpace(string(line))
		fields := strings.Fields(s)
		directive := ""
		if len(fields) > 0 {
			directive = fields[0]
		}
		switch directive {
		case "#if":
			expr := strings.TrimSpace(strings.TrimPrefix(s, "#if"))
			target := expr == symbol
			if strings.Contains(expr, symbol) && !target {
				return refuse("compound condition")
			}
			frames = append(frames, frame{target: target})
			if target {
				continue
			}
		case "#elif":
			if len(frames) == 0 {
				return refuse("unbalanced elif")
			}
			if frames[len(frames)-1].target || strings.Contains(s, symbol) {
				return refuse("elif")
			}
		case "#else":
			if s != "#else" || len(frames) == 0 {
				return refuse("ambiguous else")
			}
			f := &frames[len(frames)-1]
			if f.otherwise {
				return refuse("duplicate else")
			}
			f.otherwise = true
			if f.target {
				f.drop = true
				continue
			}
		case "#endif":
			if s != "#endif" || len(frames) == 0 {
				return refuse("unbalanced endif")
			}
			f := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			if f.target {
				continue
			}
		default:
			if strings.Contains(s, symbol) {
				return refuse("non-directive symbol occurrence")
			}
		}
		if active() {
			out.Write(line)
		}
	}
	if len(frames) > 0 {
		return refuse("unbalanced if")
	}
	return out.Bytes(), ""
}

var stackReleaseVersionAttr = regexp.MustCompile(`([A-Za-z_:][A-Za-z0-9_.:-]*)\s*=\s*("[^"]*"|'[^']*')`)

func stackReleaseXML(data []byte, pkg, version string, warnings ...string) ([]byte, bool, []string) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	last := 0
	changed := false
	var notes []string
	var conditions []bool
	for {
		start := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return data, false, []string{"invalid XML; review manually"}
		}
		end := int(d.InputOffset())
		if _, ok := tok.(xml.EndElement); ok {
			if len(conditions) > 0 {
				conditions = conditions[:len(conditions)-1]
			}
			continue
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		inCondition := len(conditions) > 0 && conditions[len(conditions)-1]
		for _, a := range el.Attr {
			if a.Name.Local == "Condition" {
				inCondition = true
			}
		}
		conditions = append(conditions, inCondition)
		for _, a := range el.Attr {
			if strings.Contains(a.Value, "RigStackSource_") || strings.Contains(a.Value, "RigStackAvailable_") {
				notes = append(notes, "source/availability condition; review manually")
			}
		}
		if el.Name.Local == "StackOnlyWarning" {
			identity := ""
			for _, a := range el.Attr {
				if a.Name.Local == "Include" {
					identity = a.Value
				}
			}
			selected := false
			for _, warning := range warnings {
				if warning == identity {
					selected = true
				}
			}
			if selected && !inCondition && len(el.Attr) == 1 && bytes.HasSuffix(bytes.TrimSpace(data[start:end]), []byte("/>")) {
				out.Write(data[last:start])
				last = end
				continue
			}
			notes = append(notes, "StackOnlyWarning has no sibling identity or is complex; use --warning for a simple declaration after manual attribution")
		}
		if el.Name.Local != "PackageReference" && el.Name.Local != "PackageVersion" {
			continue
		}
		match := false
		current := ""
		conditional := inCondition
		for _, a := range el.Attr {
			if (a.Name.Local == "Include" || a.Name.Local == "Update") && strings.EqualFold(a.Value, pkg) {
				match = true
			}
			if a.Name.Local == "Version" {
				current = a.Value
			}
			if a.Name.Local == "Condition" {
				conditional = true
			}
		}
		if !match {
			continue
		}
		if conditional {
			notes = append(notes, "unsafe package version: conditional reference")
			continue
		}
		if current == "" && !bytes.HasSuffix(bytes.TrimSpace(data[start:end]), []byte("/>")) {
			notes = append(notes, "unsafe package version: nested metadata")
			continue
		}
		if current != "" {
			old, ok1 := stackParseNugetVersion(current)
			next, ok2 := stackParseNugetVersion(version)
			if !ok1 || !ok2 || stackCompareNuget(old, next) > 0 {
				notes = append(notes, "unsafe package version: floating, unsupported or downgrade")
				continue
			}
		}
		raw := data[start:end]
		var loc []int
		for _, attr := range stackReleaseVersionAttr.FindAllSubmatchIndex(raw, -1) {
			if string(raw[attr[2]:attr[3]]) == "Version" {
				loc = []int{attr[0], attr[1], attr[4], attr[5]}
				break
			}
		}
		_, literal := stackParseNugetVersion(current)
		if conditional || !literal || loc == nil {
			notes = append(notes, "nonliteral, conditional or centrally managed reference; review manually")
			continue
		}
		out.Write(data[last:start])
		out.Write(raw[:loc[2]])
		out.WriteByte('"')
		out.WriteString(version)
		out.WriteByte('"')
		out.Write(raw[loc[3]:])
		last = end
		changed = true
	}
	out.Write(data[last:])
	return out.Bytes(), changed, notes
}

func stackReleaseEdits(root, pkg, version, symbol string, warnings ...string) ([]stackReleaseEdit, []string, error) {
	var edits []stackReleaseEdit
	var notes []string
	pinChanged := false
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
		if e.Type()&os.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".cs" && ext != ".csproj" && ext != ".props" && ext != ".targets" {
			return nil
		}
		if strings.HasSuffix(path, ".g.cs") || strings.HasSuffix(path, ".generated.cs") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(bytes.ToLower(data), []byte("<auto-generated")) {
			return nil
		}
		var after []byte
		if ext == ".cs" {
			var reason string
			after, reason = stackReleaseRewrite(data, symbol)
			if reason != "" {
				notes = append(notes, path+": "+reason)
			}
		} else {
			var changed bool
			var ns []string
			after, changed, ns = stackReleaseXML(data, pkg, version, warnings...)
			pinChanged = pinChanged || changed
			for _, n := range ns {
				if strings.HasPrefix(n, "unsafe package version") {
					return fmt.Errorf("%s: %s", path, n)
				}
				notes = append(notes, path+": "+n)
			}
		}
		if !bytes.Equal(data, after) {
			info, err := e.Info()
			if err != nil {
				return err
			}
			edits = append(edits, stackReleaseEdit{path, data, after, info.Mode()})
		}
		return nil
	})
	if err != nil {
		return nil, notes, err
	}
	if !pinChanged {
		return nil, notes, fmt.Errorf("no literal package version could be updated; refusing guard edits")
	}
	return edits, notes, nil
}

func stackReleaseWrite(edits []stackReleaseEdit) error {
	for _, e := range edits {
		b, err := os.ReadFile(e.path)
		if err != nil {
			return err
		}
		if !bytes.Equal(b, e.before) {
			return fmt.Errorf("file changed since preview: %s", e.path)
		}
	}
	for i, e := range edits {
		if err := os.WriteFile(e.path, e.after, e.mode); err != nil {
			for _, prior := range edits[:i+1] {
				_ = os.WriteFile(prior.path, prior.before, prior.mode)
			}
			return err
		}
	}
	return nil
}
