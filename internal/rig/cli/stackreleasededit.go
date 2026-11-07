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
	return stackReleaseXMLRename(data, pkg, pkg, version, warnings...)
}

func stackReleaseXMLRename(data []byte, pkg, original, version string, warnings ...string) ([]byte, bool, []string) {
	rename := !strings.EqualFold(pkg, original)
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
			if strings.EqualFold(a.Name.Local, "Condition") {
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
		if !strings.EqualFold(el.Name.Local, "PackageReference") && !strings.EqualFold(el.Name.Local, "PackageVersion") {
			continue
		}
		match := false
		current := ""
		conditional := inCondition
		override, update := false, false
		for _, a := range el.Attr {
			if (strings.EqualFold(a.Name.Local, "Include") || strings.EqualFold(a.Name.Local, "Update")) && strings.EqualFold(a.Value, pkg) {
				match = true
			}
			if strings.EqualFold(a.Name.Local, "VersionOverride") {
				override = true
			}
			if strings.EqualFold(a.Name.Local, "Update") {
				update = true
			}
			if strings.EqualFold(a.Name.Local, "Version") {
				current = a.Value
			}
			if strings.EqualFold(a.Name.Local, "Condition") {
				conditional = true
			}
		}
		if !match {
			continue
		}
		if override || update && !rename {
			notes = append(notes, "unsafe package version: VersionOverride or Update")
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
		// A versionless Include is renamed alongside its proven central pin.
		if current == "" && !rename {
			notes = append(notes, "nonliteral, conditional or centrally managed reference; review manually")
			continue
		}
		var rewritten bytes.Buffer
		offset := 0
		for _, attr := range stackReleaseVersionAttr.FindAllSubmatchIndex(raw, -1) {
			name := string(raw[attr[2]:attr[3]])
			value := ""
			switch {
			case strings.EqualFold(name, "Version"):
				value = version
			case rename && (strings.EqualFold(name, "Include") || strings.EqualFold(name, "Update")):
				value = original
			default:
				continue
			}
			rewritten.Write(raw[offset:attr[4]])
			rewritten.WriteByte('"')
			_ = xml.EscapeText(&rewritten, []byte(value))
			rewritten.WriteByte('"')
			offset = attr[5]
		}
		rewritten.Write(raw[offset:])
		out.Write(data[last:start])
		out.Write(rewritten.Bytes())
		last = end
		changed = true
	}
	out.Write(data[last:])
	return out.Bytes(), changed, notes
}

func stackReleaseEdits(root, pkg, version, symbol string, warnings ...string) ([]stackReleaseEdit, []string, error) {
	return stackReleaseRenameEdits(root, pkg, pkg, version, symbol, warnings...)
}

func stackReleaseRenameEdits(root, pkg, original, version, symbol string, warnings ...string) ([]stackReleaseEdit, []string, error) {
	if err := stackReleaseProvePins(root, pkg); err != nil {
		return nil, nil, err
	}
	var edits []stackReleaseEdit
	var notes []string
	pinChanged := false
	err := stackReleaseWalkFiles(root, func(path string) error {
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
			after, changed, ns = stackReleaseXMLRename(data, pkg, original, version, warnings...)
			pinChanged = pinChanged || changed
			for _, n := range ns {
				if strings.HasPrefix(n, "unsafe package version") {
					return fmt.Errorf("%s: %s", path, n)
				}
				notes = append(notes, path+": "+n)
			}
		}
		if !bytes.Equal(data, after) {
			info, err := os.Stat(path)
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
