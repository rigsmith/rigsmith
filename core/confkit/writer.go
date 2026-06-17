// Writer splices typed values into a JSONC config file, preserving comments,
// formatting, and key order. For an existing file the value is edited in place
// via the comment-preserving jsonc editor; only a brand-new/empty file is
// written fresh (nothing to preserve there), stamped with the tool's $schema.
//
// This generalizes rig's original ConfigWriter so every tool's `config set`
// behaves identically — the only per-tool input is the schema URL.
package confkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// Writer writes typed values into a single JSONC config file. SchemaURL is the
// $schema stamped onto a freshly written file (empty to omit the header).
type Writer struct {
	SchemaURL string
}

// SetString sets path (the key path, depth 1–2) to a JSON string.
func (w Writer) SetString(filePath string, path []string, value string) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return w.Set(filePath, path, string(raw))
}

// SetBool sets path to a JSON bool.
func (w Writer) SetBool(filePath string, path []string, value bool) bool {
	return w.Set(filePath, path, strconv.FormatBool(value))
}

// SetNumber sets path to a JSON number.
func (w Writer) SetNumber(filePath string, path []string, value float64) bool {
	return w.Set(filePath, path, strconv.FormatFloat(value, 'f', -1, 64))
}

// Document renders v as a complete JSONC config document for a whole-file write
// (a full-struct Save, or an init scaffold): an optional leading header comment
// (each line prefixed with //), the $schema key injected first, then v's fields.
// The result reads back through jsonc.Unmarshal, so the file is schema-stamped
// and comment-friendly — consistent with what the in-place Set editor preserves.
// header may be empty (no comment block); a multi-line header splits on "\n".
func (w Writer) Document(header string, v any) ([]byte, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	if header != "" {
		for _, line := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
			if line == "" {
				b.WriteString("//\n")
			} else {
				b.WriteString("// " + line + "\n")
			}
		}
	}
	s := string(body)
	if w.SchemaURL != "" && !hasTopLevelKey(body, "$schema") {
		schema := "  " + quoteJSON("$schema") + ": " + quoteJSON(w.SchemaURL)
		switch {
		case strings.HasPrefix(s, "{\n"):
			s = "{\n" + schema + ",\n" + s[len("{\n"):]
		case s == "{}":
			s = "{\n" + schema + "\n}"
		}
	}
	b.WriteString(s)
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// hasTopLevelKey reports whether the JSON object in body has key at the top
// level — checked structurally (not by substring), so a value that merely equals
// the key string doesn't count. Non-objects report false.
func hasTopLevelKey(body []byte, key string) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		return false
	}
	_, ok := obj[key]
	return ok
}

// Set splices path = rawValue (a raw JSON literal) into the file, preserving
// comments where possible. Returns false (writing nothing) when an existing,
// non-empty file can't be edited in place — it never overwrites a file that has
// real content to lose.
func (w Writer) Set(filePath string, path []string, rawValue string) bool {
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
	return os.WriteFile(filePath, []byte(w.freshDocument(path, rawValue)), 0o644) == nil
}

// freshDocument renders a brand-new config file: the $schema header (when set)
// plus the nested path = rawValue, two-space indented.
func (w Writer) freshDocument(path []string, rawValue string) string {
	var b strings.Builder
	b.WriteString("{\n")
	if w.SchemaURL != "" {
		fmt.Fprintf(&b, "  %s: %s,\n", quoteJSON("$schema"), quoteJSON(w.SchemaURL))
	}

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
