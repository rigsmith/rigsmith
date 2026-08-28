package redact

import (
	"regexp"
	"strings"
)

// textSecretRe finds credential-shaped tokens anywhere in free text, as opposed
// to LooksSecret, which judges a whole string that is already known to be one
// value. A transcript is neither: it is prose with a key buried in the middle of
// a line, which is how a pasted credential actually reaches a conversation.
//
// Deliberately limited to shapes with near-zero false positives. The
// high-entropy backstop is NOT used here: guessing wrong when judging a config
// value costs a redacted field nobody reads, but guessing wrong here rewrites
// the middle of somebody's conversation — a base64 blob, a hash, a code sample —
// and the original is not kept. Precision matters more than recall when the
// edit is silent and the fallback is a refusal that at least tells you.
var textSecretRe = regexp.MustCompile(strings.Join([]string{
	// Known vendor prefixes followed by enough body to be a real key. Kept in
	// step with knownPrefixes by TestTextRulesCoverKnownPrefixes.
	`sk-ant-[A-Za-z0-9_\-]{8,}`,
	`sk-[A-Za-z0-9_\-]{16,}`,
	`gh[pousr]_[A-Za-z0-9]{8,}`,
	`github_pat_[A-Za-z0-9_]{8,}`,
	`glpat-[A-Za-z0-9_\-]{8,}`,
	`xox[bpar]-[A-Za-z0-9\-]{8,}`,
	`(?:AKIA|ASIA)[A-Z0-9]{12,}`,
	`AIza[A-Za-z0-9_\-]{8,}`,
	`ya29\.[A-Za-z0-9_\-]{8,}`,
	// A JWT, anchored on its header rather than on the whole string.
	`eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}`,
}, "|"))

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
	locs := textSecretRe.FindAllIndex(data, -1)
	if len(locs) == 0 {
		return data, nil, false
	}
	var b strings.Builder
	b.Grow(len(data))
	prev := 0
	for _, loc := range locs {
		match := string(data[loc[0]:loc[1]])
		b.Write(data[prev:loc[0]])
		b.WriteString(Placeholder)
		prev = loc[1]
		hits = append(hits, TextHit{Kind: kindOf(match), Hint: hint(match)})
	}
	b.Write(data[prev:])
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
func HasPrivateKey(line []byte) bool { return pemRe.Match(line) }
