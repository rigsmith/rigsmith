package mergepolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// ResolveMetadata uses the existing native unions for retained publication.
// Only schema-1 manifest/device documents are accepted. Unknown/duplicate fields,
// unsupported paths and malformed sides remain conflicts, never newest-side
// fallbacks. Base is not used: these metadata maps describe an additive union.
// The publisher bounds inputs/outputs and audits the complete candidate.
func ResolveMetadata(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var merged any
	switch path {
	case manifest.FileName:
		var a, b manifest.Manifest
		if !decodeRetainedMetadata(ours, &a) || !decodeRetainedMetadata(theirs, &b) || a.Schema != 1 || b.Schema != 1 {
			return nil, commitartifact.ErrConflict
		}
		merged = mergeManifest(a, b)
	case devices.FileName:
		var a, b devices.Registry
		if !decodeRetainedMetadata(ours, &a) || !decodeRetainedMetadata(theirs, &b) || a.Schema != 1 || b.Schema != 1 {
			return nil, commitartifact.ErrConflict
		}
		merged = mergeDevices(a, b)
	default:
		return nil, commitartifact.ErrConflict
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decodeRetainedMetadata(raw []byte, out any) bool {
	if !utf8.Valid(raw) {
		return false
	}
	// DisallowUnknownFields does not reject duplicate fields. Validate tokens
	// first so a duplicate (including a case alias) cannot silently lose a value.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	if !uniqueJSON(tokens, 0) {
		return false
	}
	if _, err := tokens.Token(); err != io.EOF {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out) == nil
}

func uniqueJSON(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return false
			}
			name = strings.ToLower(name)
			if seen[name] {
				return false
			}
			seen[name] = true
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	case '[':
		for d.More() {
			if !uniqueJSON(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, err := d.Token()
	return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
}
