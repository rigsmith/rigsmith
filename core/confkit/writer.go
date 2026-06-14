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
