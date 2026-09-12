// Package codec is the structured-format seam the sync engine dispatches on.
//
// clauderig's entire format dispatch is one line — `strings.HasSuffix(rel,
// ".json")` — because Claude Code's configuration is all JSON. Codex's is TOML,
// and the wrong thing to do about that is add a second suffix test beside the
// first: a raw-file path for TOML would carry config.toml through verbatim,
// bypassing field-level redaction and the secret-preserving restore, which is
// how a credential in an [mcp_servers.x.env] table reaches a git remote.
//
// So a format is a Codec: it decodes to the same generic tree the redactor and
// the path rewriter already understand (maps, slices, scalars), and encodes back
// deterministically. Everything between those two calls is format-blind.
package codec

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Codec reads and writes one structured configuration format.
type Codec interface {
	// Name is the format, for messages.
	Name() string
	// Decode parses bytes into the generic tree (map[string]any / []any /
	// scalars) that redact and pathmap operate on.
	Decode(data []byte) (any, error)
	// Encode renders the tree back. It MUST be deterministic for equal input:
	// the engine byte-compares the result against what is already staged to
	// decide whether anything changed, and an encoder that reorders keys would
	// report every file as modified on every sync.
	Encode(v any) ([]byte, error)
}

// For returns the codec for a file, and whether there is one. A file with no
// codec is carried verbatim — which is right for a rollout or a skill, and would
// be wrong for a configuration format, so adding a format here is how it becomes
// safe to include in the allowlist.
func For(rel string) (Codec, bool) {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".json":
		return JSON{}, true
	case ".toml":
		return TOML{}, true
	default:
		return nil, false
	}
}

// JSON is the JSON codec.
type JSON struct{}

func (JSON) Name() string { return "json" }

func (JSON) Decode(data []byte) (any, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (JSON) Encode(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// TOML is the TOML codec.
//
// One property is worth stating because it is a real cost rather than an
// oversight: comments do not survive a decode/encode round trip. In the staged
// copy that is harmless — it is a derived file nobody edits. On RESTORE it would
// mean overwriting a commented local config with an uncommented one, so the
// restore path compares semantically first and writes nothing when the merged
// result already matches what is on disk. A machine whose config has not changed
// upstream therefore keeps its comments; one that has genuinely changed loses
// them, and that is said out loud in the restore output rather than discovered.
type TOML struct{}

func (TOML) Name() string { return "toml" }

func (TOML) Decode(data []byte) (any, error) {
	var v map[string]any
	if err := toml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	if v == nil {
		// An empty document decodes to a nil map, which would marshal as the
		// four bytes "null" through some encoders. Normalise it to an empty
		// table so an empty file round-trips as an empty file.
		v = map[string]any{}
	}
	return v, nil
}

func (TOML) Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	// Tables after scalars, and each nested table written once in full: without
	// it the encoder can emit a key after the table header it belongs under,
	// which is a different document.
	enc.SetTablesInline(false)
	enc.SetIndentTables(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}
