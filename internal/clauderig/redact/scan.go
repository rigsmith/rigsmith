package redact

import (
	"encoding/json"
	"sort"

	"github.com/rigsmith/rigsmith/internal/agentrig/secrets"
)

// Finding is one string value that looks like a credential.
type Finding struct {
	Path string // dotted JSON path
	Kind string // why it tripped (e.g. "anthropic-key", "jwt", "high-entropy")
}

// LooksSecret retains Claude's placeholder exemption and detection behavior.
func LooksSecret(s string) (string, bool) {
	if s == Placeholder {
		return "", false
	}
	return secrets.LooksSecret(s)
}

// Scan walks parsed JSON and reports string values that look like credentials.
// Run it on the *redacted* document as a tripwire: any finding means a secret got
// past the key rules. Returns findings sorted by path.
func Scan(v any) []Finding {
	var out []Finding
	scanNode(v, "", &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ScanBytes is Scan over a JSON document.
func ScanBytes(data []byte) ([]Finding, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return Scan(v), nil
}

func scanNode(node any, path string, out *[]Finding) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			scanNode(v, joinPath(path, k), out)
		}
	case []any:
		for i, v := range n {
			scanNode(v, joinIndex(path, i), out)
		}
	case string:
		if kind, ok := LooksSecret(n); ok {
			*out = append(*out, Finding{Path: path, Kind: kind})
		}
	}
}
