package mergepolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
)

// ResolveRetained adds conservative append recovery to the metadata policy.
// The publisher supplies bounded blobs and audits the complete resulting tree.
// Text needs an existing base preserved verbatim by both sides; edits, truncation,
// add/add text and chunk indexes remain conflicts. Synchronous policy is unchanged.
func ResolveRetained(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rule := adapter.ClassifyMerge(path)
	if rule.Strategy != adapter.UnionText {
		return ResolveMetadata(ctx, path, base, ours, theirs)
	}
	if base == nil || ours == nil || theirs == nil {
		return nil, commitartifact.ErrConflict
	}
	for _, side := range [][]byte{base, ours, theirs} {
		if !utf8.Valid(side) || bytes.IndexByte(side, 0) >= 0 || (len(side) > 0 && side[len(side)-1] != '\n') {
			return nil, commitartifact.ErrConflict
		}
	}
	if !bytes.HasPrefix(ours, base) || !bytes.HasPrefix(theirs, base) {
		return nil, commitartifact.ErrConflict
	}
	// A common append may already be present on both machines. Keep their
	// longest shared prefix of complete lines once, then local and remote tails.
	shared := len(base)
	for i := shared; i < min(len(ours), len(theirs)) && ours[i] == theirs[i]; i++ {
		if ours[i] == '\n' {
			shared = i + 1
		}
	}
	merged := append([]byte(nil), ours...)
	merged = append(merged, theirs[shared:]...)
	if !rule.DeduplicateRecords {
		return merged, ctx.Err()
	}
	return retainedRecords(ctx, merged)
}

// retainedRecords preserves raw JSONL bytes and unkeyed records, deduplicating
// only identical records with the same UUID. Different payloads for one UUID
// must remain a conflict: silently choosing one would discard a recorded turn.
func retainedRecords(ctx context.Context, content []byte) ([]byte, error) {
	seen := map[string]string{}
	var out bytes.Buffer
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := bytes.IndexByte(content, '\n') + 1 // callers require complete lines
		line := content[:n]
		content = content[n:]
		record := bytes.TrimSpace(line)
		if len(record) == 0 {
			out.Write(line)
			continue
		}
		uuid, ok := retainedRecordUUID(record)
		if !ok {
			return nil, commitartifact.ErrConflict
		}
		if uuid != "" {
			if previous, found := seen[uuid]; found {
				if previous != string(record) {
					return nil, commitartifact.ErrConflict
				}
				continue
			}
			seen[uuid] = string(record)
		}
		out.Write(line)
	}
	return out.Bytes(), ctx.Err()
}

// Inspect top-level fields without reserializing or discarding unknown payloads.
// Duplicate fields and UUID aliases are ambiguous. A chunk marker is never a
// transcript record, even if it is reordered or has a different case spelling.
func retainedRecordUUID(raw []byte) (string, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return "", false
	}
	seen := map[string]bool{}
	var uuid string
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return "", false
		}
		seen[key] = true
		if strings.EqualFold(key, "clauderig_chunked_transcript") || (strings.EqualFold(key, "uuid") && key != "uuid") {
			return "", false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return "", false
		}
		if key == "uuid" {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &uuid) != nil {
				return "", false
			}
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') {
		return "", false
	}
	_, err = d.Token()
	return uuid, err == io.EOF
}
