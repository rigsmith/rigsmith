package mergepolicy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

const retainedTranscriptLimit = 32 << 20

// ResolveRetainedFiles also recovers append conflicts between canonical v1 chunk
// indexes. It verifies each side's immutable parts, applies the native record
// policy, then proposes new chunks before returning their index. Native/chunked
// conversion conflicts, edited history and oversized transcripts remain blocked.
func ResolveRetainedFiles(ctx context.Context, path string, base, ours, theirs []byte, files commitartifact.RelatedFiles) ([]byte, error) {
	if !transcript.IsIndex(base) && !transcript.IsIndex(ours) && !transcript.IsIndex(theirs) {
		return ResolveRetained(ctx, path, base, ours, theirs)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, rel, _ := strings.Cut(path, "/")
	if root != "cli" || adapter.Classify(root, rel).Kind != adapter.Transcript || transcript.IsPartPath(rel) || files == nil {
		return nil, commitartifact.ErrConflict
	}
	var indexes [3]*transcript.Index
	for i, raw := range [][]byte{base, ours, theirs} {
		idx, err := transcript.Decode(raw)
		if err != nil || idx == nil || idx.Parts == nil {
			return nil, commitartifact.ErrConflict
		}
		// Accept exactly the schema emitted by the native writer. Do not silently
		// discard unknown/duplicate fields, aliases or a future index representation.
		canonical, err := json.Marshal(idx)
		if err != nil || !bytes.Equal(bytes.Trim(raw, " \t\r\n"), canonical) {
			return nil, commitartifact.ErrConflict
		}
		if idx.Size > retainedTranscriptLimit {
			return nil, artifact.ErrTooLarge
		}
		indexes[i] = idx
	}
	if indexes[1].Size < indexes[0].Size || indexes[2].Size < indexes[0].Size {
		return nil, commitartifact.ErrConflict
	}
	// Bound the worst-case union before loading parts or allocating record maps.
	if indexes[1].Size+indexes[2].Size-indexes[0].Size > retainedTranscriptLimit {
		return nil, artifact.ErrTooLarge
	}
	var logical [3][]byte
	for i, raw := range [][]byte{base, ours, theirs} {
		data, err := transcript.ReadStored(path, raw, func(part string) ([]byte, error) {
			return files.Read(ctx, commitartifact.ConflictSide(i), part)
		}, 0)
		if err != nil {
			return nil, err
		}
		// Preserve a present, empty base (ReadStored may return a nil empty slice).
		if data == nil {
			data = []byte{}
		}
		logical[i] = data
	}
	merged, err := ResolveRetained(ctx, path, logical[0], logical[1], logical[2])
	if err != nil {
		return nil, err
	}
	idx := transcript.Index{Version: 1, Size: int64(len(merged)), Parts: []transcript.Part{}}
	for len(merged) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(len(merged), transcript.ChunkSize)
		part := merged[:n]
		merged = merged[n:]
		hash := fmt.Sprintf("%x", sha256.Sum256(part))
		if err := files.Add(ctx, path+transcript.Suffix+"/"+hash+".part", part); err != nil {
			return nil, err
		}
		idx.Parts = append(idx.Parts, transcript.Part{Hash: hash, Size: n})
	}
	encoded, err := json.Marshal(idx)
	return append(encoded, '\n'), err
}
