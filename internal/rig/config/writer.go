// Writer lets `rig` manage a .rig.json (the repo's or the user-wide one) so
// users never hand-edit for the common case. The file-level splice mechanics
// live in core/confkit (shared with the other tools' `config set`); this file
// is the thin rig-specific layer that pins the .rig.json $schema and the
// repo-relative path.
package config

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/rigsmith/rigsmith/core/confkit"
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

// AddRepoExclude adds glob to the repo .rig.json `exclude` array (deduped,
// sorted) and returns the config path and whether the write landed. A no-op
// (glob already present) still reports ok=true with the unchanged file. current
// is the already-merged exclude list so the caller need not re-read.
func AddRepoExclude(root string, current []string, glob string) (path string, ok bool) {
	for _, g := range current {
		if g == glob {
			return filepath.Join(root, FileName), true
		}
	}
	return setRepoExclude(root, append(append([]string{}, current...), glob))
}

// RemoveRepoExclude drops every entry equal to glob from the repo .rig.json
// `exclude` array. A no-op (glob absent) reports ok=true.
func RemoveRepoExclude(root string, current []string, glob string) (path string, ok bool) {
	var next []string
	removed := false
	for _, g := range current {
		if g == glob {
			removed = true
			continue
		}
		next = append(next, g)
	}
	if !removed {
		return filepath.Join(root, FileName), true
	}
	return setRepoExclude(root, next)
}

// setRepoExclude writes the repo .rig.json `exclude` array (deduped + sorted for
// a stable diff), splicing into an existing file or creating a fresh one.
func setRepoExclude(root string, globs []string) (path string, ok bool) {
	path = filepath.Join(root, FileName)
	seen := map[string]bool{}
	var uniq []string
	for _, g := range globs {
		if g != "" && !seen[g] {
			seen[g] = true
			uniq = append(uniq, g)
		}
	}
	sort.Strings(uniq)
	raw, err := json.Marshal(uniq)
	if err != nil {
		return path, false
	}
	return path, Set(path, []string{"exclude"}, string(raw))
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
