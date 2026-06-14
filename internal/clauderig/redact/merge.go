package redact

import "encoding/json"

// Merge overlays a synced (redacted) JSON value onto the local one. Every field
// is taken from synced EXCEPT a value equal to Placeholder, which keeps local's
// value — so restoring redacted config never clobbers the machine's real secret
// with a placeholder. On a fresh machine (no local value) a placeholder field is
// dropped entirely rather than written as a literal. Local-only keys are kept
// (machine-specific additions survive a restore).
func Merge(synced, local any) any {
	if sm, ok := synced.(map[string]any); ok {
		lm, _ := local.(map[string]any)
		out := make(map[string]any, len(sm))
		for k, sv := range sm {
			lv, hasLocal := lm[k]
			if isPlaceholder(sv) {
				if hasLocal {
					out[k] = lv // keep the local secret
				}
				continue // fresh machine: drop the placeholder
			}
			// Always recurse (lv is nil when absent) so nested placeholders are
			// dropped/kept correctly even where the local side has no counterpart.
			out[k] = Merge(sv, lv)
		}
		for k, lv := range lm {
			if _, in := sm[k]; !in {
				out[k] = lv // preserve local-only keys
			}
		}
		return out
	}
	if sa, ok := synced.([]any); ok {
		la, _ := local.([]any)
		out := make([]any, len(sa))
		for i, sv := range sa {
			var lv any
			if i < len(la) {
				lv = la[i]
			}
			out[i] = Merge(sv, lv)
		}
		return out
	}
	if isPlaceholder(synced) {
		return local
	}
	return synced
}

func isPlaceholder(v any) bool {
	s, ok := v.(string)
	return ok && s == Placeholder
}

// MergeBytes merges a synced JSON document onto a local one (local may be nil for
// a fresh machine). Output is indented, deterministic JSON.
func MergeBytes(syncedJSON, localJSON []byte) ([]byte, error) {
	var synced, local any
	if err := json.Unmarshal(syncedJSON, &synced); err != nil {
		return nil, err
	}
	if len(localJSON) > 0 {
		if err := json.Unmarshal(localJSON, &local); err != nil {
			return nil, err
		}
	}
	merged := Merge(synced, local)
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
