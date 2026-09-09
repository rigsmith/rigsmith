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
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// ResolveRetained adds conservative append recovery to the metadata policy.
// The publisher supplies bounded blobs and audits the complete resulting tree.
// Text needs an existing base preserved verbatim by both sides; per-machine
// journals also allow independently created files. Edits, truncation, other
// add/add text and chunk indexes remain conflicts. Synchronous policy is unchanged.
func ResolveRetained(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rule := adapter.ClassifyMerge(path)
	if rule.Strategy != adapter.UnionText {
		return ResolveMetadata(ctx, path, base, ours, theirs)
	}
	// Retained recovery is narrower than synchronous extension-based union:
	// Native CLI transcripts, memory text and per-machine journals have explicit
	// append contracts. Journals can also start independently from an absent base.
	root, rel, _ := strings.Cut(path, "/")
	file := adapter.Classify(root, rel)
	isJournal := root == journal.DirName && !strings.ContainsAny(rel, "/\\") && strings.HasSuffix(rel, ".jsonl") && len(rel) > len(".jsonl")
	if !isJournal && (root != "cli" || transcript.IsPartPath(rel) ||
		(rule.DeduplicateRecords && file.Kind != adapter.Transcript) ||
		(!rule.DeduplicateRecords && file.Kind != adapter.Memory)) {
		return nil, commitartifact.ErrConflict
	}
	if isJournal {
		for _, side := range [][]byte{base, ours, theirs} {
			if !journalRecords(side, rel) {
				return nil, commitartifact.ErrConflict
			}
		}
		if base == nil {
			base = []byte{}
		}
		// Journal UUID-like fields are opaque future data, not transcript IDs.
		rule.DeduplicateRecords = false
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
	if rule.DeduplicateRecords {
		return retainedRecords(ctx, ours, theirs[shared:], shared)
	}
	merged := append([]byte(nil), ours...)
	return append(merged, theirs[shared:]...), ctx.Err()
}

// retainedRecords preserves each side's records, including existing duplicates.
// Only proven cross-side UUID matches can be removed from the incoming tail.
// Conflicting payloads remain blocked, including within one input snapshot.
func retainedRecords(ctx context.Context, ours, incoming []byte, shared int) ([]byte, error) {
	local := map[string]string{}
	seen := map[string]string{}
	var out bytes.Buffer
	for side, content := range [][]byte{ours, incoming} {
		offset := 0
		for len(content) > 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n := bytes.IndexByte(content, '\n') + 1
			if n == 0 {
				return nil, commitartifact.ErrConflict
			}
			line := content[:n]
			content = content[n:]
			start := offset
			offset += n
			// Trim only JSON whitespace; every emitted byte stays unchanged.
			record := bytes.Trim(line, " \t\r\n")
			if len(record) == 0 {
				out.Write(line)
				continue
			}
			uuid, ok := retainedRecordUUID(record)
			if !ok {
				return nil, commitartifact.ErrConflict
			}
			if uuid != "" {
				id := retainedUUIDIdentity(uuid)
				if previous, found := seen[id]; found && previous != string(record) {
					return nil, commitartifact.ErrConflict
				}
				seen[id] = string(record)
				if side == 0 && start >= shared {
					local[id] = string(record)
				} else if side == 1 {
					if _, found := local[id]; found {
						continue
					}
				}
			}
			out.Write(line)
		}
	}
	return out.Bytes(), ctx.Err()
}

// Standard hyphenated UUIDs have case-insensitive hex identity. Preserve opaque
// legacy IDs exactly. A spelling change still changes raw payload bytes and
// therefore blocks recovery; it never silently selects one spelling.
func retainedUUIDIdentity(id string) string {
	if len(id) != 36 {
		return id
	}
	for i := range len(id) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if id[i] != '-' {
				return id
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", rune(id[i])) {
			return id
		}
	}
	return strings.ToLower(id)
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
			if bytes.Equal(bytes.Trim(value, " \t\r\n"), []byte("null")) || json.Unmarshal(value, &uuid) != nil {
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

// Journal appends must contain actual records for the named machine. Unknown
// fields remain byte-preserved, but malformed records cannot establish an append.
func journalRecords(data []byte, name string) bool {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return false
		}
		line := data[:i]
		data = data[i+1:]
		var record journal.Record
		if !uniqueJournalFields(line) || json.Unmarshal(line, &record) != nil || record.At.IsZero() || !journal.MatchesFile(name, record.Machine) {
			return false
		}
		switch record.Op {
		case journal.OpSync, journal.OpPull, journal.OpRestore, journal.OpMerge:
		default:
			return false
		}
		switch record.Outcome {
		case journal.OutcomeOK, journal.OutcomeFailed, journal.OutcomeRefused:
		default:
			return false
		}
	}
	return true
}

// Keep duplicate-field rejection independent of transcript UUID/index rules.
func uniqueJournalFields(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return err == io.EOF
}
