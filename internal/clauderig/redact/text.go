package redact

import (
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/secrets"
)

// TextHit is one credential found in free text.
type TextHit struct {
	Kind string // the rule that matched, e.g. "anthropic-key"
	// Hint is the first few characters, enough to recognise which key it was
	// without reproducing it. Never the whole value: this is written into the
	// journal, which is synced.
	Hint string
}

// RedactText replaces credential-shaped tokens in free text with Placeholder,
// returning the cleaned bytes and what it found. Returns changed=false and the
// input untouched when there is nothing to do, so callers can keep their
// unchanged-file fast path.
//
// A PEM private key is reported but NOT replaced: it spans many lines with a
// structure this cannot safely rewrite, and a half-scrubbed key is worse than an
// honest refusal.
func RedactText(data []byte) (out []byte, hits []TextHit, changed bool) {
	if len(data) == 0 {
		return data, nil, false
	}
	locs := secrets.TextMatches(data)
	if len(locs) == 0 {
		return data, nil, false
	}
	var b strings.Builder
	b.Grow(len(data))
	prev := 0
	for _, loc := range locs {
		match := string(data[loc[0]:loc[1]])
		if !IsCredentialMatch(match) {
			continue
		}
		b.Write(data[prev:loc[0]])
		b.WriteString(Placeholder)
		prev = loc[1]
		hits = append(hits, TextHit{Kind: kindOf(match), Hint: hint(match)})
	}
	b.Write(data[prev:])
	if len(hits) == 0 {
		return data, nil, false
	}
	return []byte(b.String()), hits, true
}

// kindOf names a match using the same vocabulary as LooksSecret, so a hit reads
// the same wherever it surfaces.
func kindOf(s string) string {
	if k, ok := LooksSecret(s); ok {
		return k
	}
	return "credential"
}

// hint is the leading characters of a secret — enough to tell two keys apart,
// short enough not to be one. Vendor prefixes are the recognisable part and are
// public by design.
func hint(s string) string {
	const n = 10
	if len(s) <= n {
		return s[:len(s)/2] + "…"
	}
	return s[:n] + "…"
}

// HasPrivateKey reports a PEM private-key header. Kept separate from RedactText
// because a key block is the one shape that must NOT be rewritten in place: it
// spans a structure this cannot safely edit, and half a scrubbed key is worse
// than a refusal that says what it found.
func HasPrivateKey(line []byte) bool { return secrets.HasPrivateKey(line) }

// IsCredentialMatch shares the detector's existing prose exclusions.
func IsCredentialMatch(match string) bool { return secrets.IsCredentialMatch(match) }
