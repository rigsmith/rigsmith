package redact

import "encoding/json"

// Merge overlays a synced (redacted) JSON value onto the local one. Every field
// is taken from synced EXCEPT a value equal to Placeholder, which keeps local's
// value — so restoring redacted config never clobbers the machine's real secret
// with a placeholder. On a fresh machine (no local value) a placeholder field is
// dropped entirely rather than written as a literal. Local-only keys are kept
// (machine-specific additions survive a restore).
func Merge(synced, local any) any {
	sm, sok := synced.(map[string]any)
	lm, lok := local.(map[string]any)
	if sok {
		if !lok {
			lm = map[string]any{}
		}
		out := make(map[string]any, len(sm))
		for k, sv := range sm {
			lv, hasLocal := lm[k]
			if isPlaceholder(sv) {
				if hasLocal {
					out[k] = lv // keep the local secret
				}
				continue // fresh machine: drop the placeholder
			}
			if hasLocal {
				out[k] = Merge(sv, lv)
			} else {
				out[k] = sv
			}
		}
		for k, lv := range lm {
			if _, in := sm[k]; !in {
				out[k] = lv // preserve local-only keys
			}
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
