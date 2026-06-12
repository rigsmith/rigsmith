// Writer lets `rig` manage a .rig.json (the repo's or the user-wide one) so
// users never hand-edit for the common case. For an existing file the value is
// spliced in place via the comment-preserving jsonc editor, keeping comments,
// formatting, and key order; only a brand-new/empty file is written fresh
// (nothing to preserve there). Values are typed — string, bool, or number.
//
// This is the Go port of the .NET rig's ConfigWriter.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rigsmith/core/jsonc"
)

// SchemaURL is the $schema stamped onto freshly written .rig.json files.
const SchemaURL = "https://rigsmith.dev/schemas/rig.json"

// SetRepoString sets a top-level string in the repo's .rig.json, returning the
// config path. Mirrors the .NET ConfigWriter.SetString(root, property, value).
func SetRepoString(root, property, value string) string {
	path := filepath.Join(root, FileName)
	SetString(path, []string{property}, value)
	return path
}

// SetString sets path (depth 1–2) to a JSON string in the file at filePath.
func SetString(filePath string, path []string, value string) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return Set(filePath, path, string(raw))
}

// SetBool sets path (depth 1–2) to a JSON bool in the file at filePath.
func SetBool(filePath string, path []string, value bool) bool {
	return Set(filePath, path, strconv.FormatBool(value))
}

// SetNumber sets path (depth 1–2) to a JSON number in the file at filePath.
func SetNumber(filePath string, path []string, value float64) bool {
	return Set(filePath, path, strconv.FormatFloat(value, 'f', -1, 64))
}

// Set splices path = rawValue (a raw JSON literal) into the file, preserving
// comments where possible. Returns false (writing nothing) when an existing,
// non-empty file can't be edited in place — we never overwrite a file that has
// real content to lose.
func Set(filePath string, path []string, rawValue string) bool {
	if len(path) == 0 {
		return false
	}

	existing, err := os.ReadFile(filePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false // unreadable existing file — don't risk clobbering it
	}
	if err == nil && strings.TrimSpace(string(existing)) != "" {
		// A real file: splice in place, or refuse rather than clobber it.
		edited, ok := jsonc.Set(string(existing), path, rawValue)
		if !ok {
			return false
		}
		return os.WriteFile(filePath, []byte(edited), 0o644) == nil
	}

	// No file (or an empty/whitespace one): safe to write a fresh document.
	if dir := filepath.Dir(filePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false
		}
	}
	return os.WriteFile(filePath, []byte(freshDocument(path, rawValue)), 0o644) == nil
}

// freshDocument renders a brand-new .rig.json: the $schema header plus the
// nested path = rawValue, two-space indented (the .NET WriteIndented shape).
func freshDocument(path []string, rawValue string) string {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  %s: %s,\n", quoteJSON("$schema"), quoteJSON(SchemaURL))

	indent := "  "
	for i, key := range path {
		if i < len(path)-1 {
			fmt.Fprintf(&b, "%s%s: {\n", indent, quoteJSON(key))
			indent += "  "
		} else {
			fmt.Fprintf(&b, "%s%s: %s\n", indent, quoteJSON(key), rawValue)
		}
	}
	for range path[1:] {
		indent = indent[:len(indent)-2]
		b.WriteString(indent + "}\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// quoteJSON renders s as a JSON string literal.
func quoteJSON(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}
