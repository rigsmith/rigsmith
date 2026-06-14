// Writer lets `rig` manage a .rig.json (the repo's or the user-wide one) so
// users never hand-edit for the common case. The file-level splice mechanics
// live in core/confkit (shared with the other tools' `config set`); this file
// is the thin rig-specific layer that pins the .rig.json $schema and the
// repo-relative path.
package config

import (
	"path/filepath"

	"github.com/rigsmith/core/confkit"
)

// SchemaURL is the $schema stamped onto freshly written .rig.json files.
const SchemaURL = "https://rigsmith.dev/schemas/rig.json"

// writer is the shared JSONC writer pinned to rig's schema.
var writer = confkit.Writer{SchemaURL: SchemaURL}

// SetRepoString sets a top-level string in the repo's .rig.json, returning the
// config path and whether the write actually landed. ok is false when SetString
// declined to edit an existing non-empty file in place (see Set) — callers must
// not report success in that case. Mirrors the .NET
// ConfigWriter.SetString(root, property, value).
func SetRepoString(root, property, value string) (path string, ok bool) {
	path = filepath.Join(root, FileName)
	ok = SetString(path, []string{property}, value)
	return path, ok
}

// SetString sets path (depth 1–2) to a JSON string in the file at filePath.
func SetString(filePath string, path []string, value string) bool {
	return writer.SetString(filePath, path, value)
}

// SetBool sets path (depth 1–2) to a JSON bool in the file at filePath.
func SetBool(filePath string, path []string, value bool) bool {
	return writer.SetBool(filePath, path, value)
}

// SetNumber sets path (depth 1–2) to a JSON number in the file at filePath.
func SetNumber(filePath string, path []string, value float64) bool {
	return writer.SetNumber(filePath, path, value)
}

// Set splices path = rawValue (a raw JSON literal) into the file, preserving
// comments where possible. Returns false (writing nothing) when an existing,
// non-empty file can't be edited in place — we never overwrite a file that has
// real content to lose.
func Set(filePath string, path []string, rawValue string) bool {
	return writer.Set(filePath, path, rawValue)
}
