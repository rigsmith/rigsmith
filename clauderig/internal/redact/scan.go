package redact

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Finding is one string value that looks like a credential.
type Finding struct {
	Path string // dotted JSON path
	Kind string // why it tripped (e.g. "anthropic-key", "jwt", "high-entropy")
}

// knownPrefixes are near-zero-false-positive credential shapes.
var knownPrefixes = []struct {
	kind   string
	prefix string
}{
	{"anthropic-key", "sk-ant-"},
	{"openai-key", "sk-"},
	{"github-token", "ghp_"},
	{"github-token", "gho_"},
	{"github-token", "ghs_"},
	{"github-token", "ghu_"},
	{"github-token", "ghr_"},
	{"github-pat", "github_pat_"},
	{"gitlab-pat", "glpat-"},
	{"slack-token", "xoxb-"},
	{"slack-token", "xoxp-"},
	{"slack-token", "xoxa-"},
	{"slack-token", "xoxr-"},
	{"aws-key", "AKIA"},
	{"aws-key", "ASIA"},
	{"google-key", "AIza"},
	{"google-oauth", "ya29."},
}

var (
	uuidRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	jwtRe     = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}$`)
	pemRe     = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	tokenChar = regexp.MustCompile(`^[A-Za-z0-9+/=_\-]+$`)
)

// LooksSecret reports whether s has the shape of a credential, and the kind. It
// errs toward avoiding false positives: known token prefixes / JWT / PEM are
// flagged with high confidence; otherwise a long, single-token, high-entropy
// string trips "high-entropy" — but UUIDs and the redaction Placeholder never do.
func LooksSecret(s string) (kind string, ok bool) {
	if s == "" || s == Placeholder {
		return "", false
	}
	if pemRe.MatchString(s) {
		return "private-key", true
	}
	if strings.HasPrefix(s, "Bearer ") && len(s) > 20 {
		return "bearer", true
	}
	for _, p := range knownPrefixes {
		if strings.HasPrefix(s, p.prefix) && len(s) >= len(p.prefix)+8 {
			return p.kind, true
		}
	}
	if jwtRe.MatchString(s) {
		return "jwt", true
	}
	// Generic high-entropy token: a single opaque blob, not a UUID, not a path.
	if uuidRe.MatchString(s) {
		return "", false
	}
	if len(s) >= 40 && tokenChar.MatchString(s) && shannonBits(s) >= 3.5 {
		return "high-entropy", true
	}
	return "", false
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

// shannonBits returns the Shannon entropy of s in bits per character.
func shannonBits(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]float64
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := c / n
		h -= p * math.Log2(p)
	}
	return h
}
