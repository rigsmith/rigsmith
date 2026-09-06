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
	// Every rule is anchored on a word boundary. Without one they match INSIDE
	// ordinary words: `sk-` is the tail of "task-", "risk-" and "desk-", so
	// "global-task-runner-config" reads as an OpenAI key, and AKIA/ASIA match
	// anywhere inside a longer uppercase or base64 run. Measured on one real
	// machine, 96 of 134 findings were exactly this and every one of them
	// refused a sync.
	`\bsk-ant-[A-Za-z0-9_\-]{8,}`,
	`\bsk-[A-Za-z0-9_\-]{16,}`,
	`\bgh[pousr]_[A-Za-z0-9]{8,}`,
	`\bgithub_pat_[A-Za-z0-9_]{8,}`,
	`\bglpat-[A-Za-z0-9_\-]{8,}`,
	`\bxox[bpar]-[A-Za-z0-9\-]{8,}`,
	// Exactly sixteen, and bounded both ends: an AWS access key id is twenty
	// characters. Open-ended, this matched any shouted phrase containing the
	// letters — ASIAPACIFICREGIONSETTINGS and the like.
	`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`,
	`\bAIza[A-Za-z0-9_\-]{8,}`,
	`\bya29\.[A-Za-z0-9_\-]{8,}`,
	// A JWT, anchored on its header rather than on the whole string.
	`\beyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}`,
	// An opaque bearer token. LooksSecret already calls one of these a
	// credential when it judges a config value, and leaving it in a transcript
	// while redacting it from settings is the inconsistency, not the rule.
	// Bounded hard, because `Bearer` also appears in every API example ever
	// pasted into a chat: the RFC 6750 token charset, at least 24 characters,
	// and placeholders are dropped below.
	`(?i:\bBearer)\s+[A-Za-z0-9\-._~+/]{24,}={0,2}`,
}, "|"))

// kebabProseRe matches a hyphenated run of lowercase words: "sk-a-single-line",
// "sk-the-window-has-no-repo". Anchoring stops the rules firing inside a word,
// but not on prose that genuinely begins with one of the prefixes — and a
// conversation about this very tool is full of it.
//
// Safe to exclude because no issued credential looks like this. Every vendor
// here mints keys from a base62 or base64 alphabet, so a real one carries
// digits or capitals within a few characters; a body that is nothing but
// lowercase letters and hyphens is a sentence.
var kebabProseRe = regexp.MustCompile(`^[a-z]+(?:-[a-z]+)+$`)

// screamingRe matches a bearer token that is really a placeholder —
// YOUR_ACCESS_TOKEN, REPLACE_ME_WITH_TOKEN. Real tokens are mixed case; the
// things people paste into examples are not.
var screamingRe = regexp.MustCompile(`^(?i:Bearer)\s+[A-Z0-9_]+={0,2}$`)

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
func HasPrivateKey(line []byte) bool { return pemRe.Match(line) }

// IsCredentialMatch reports whether a regex hit is really a credential rather
// than something merely shaped like one.
//
// Shared deliberately. RedactText uses it to decide what to rewrite and the
// stream scanner to decide what to refuse, and if the two disagree the tool
// either rewrites something it will still refuse — leaving a sync blocked with
// the scrubber already on and nothing left to try — or refuses something it has
// already cleaned. Both were live: the prose exclusion landed in the rewriter
// only, so the tripwire kept refusing the phrases the rewriter had decided to
// leave alone.
func IsCredentialMatch(match string) bool {
	return !screamingRe.MatchString(match) && !isKebabProse(match)
}

// isKebabProse reports whether a match is a hyphenated lowercase phrase rather
// than a credential. Judged on the body, after the vendor prefix: "sk-" is
// followed by a key in every real case and by English in the false ones.
func isKebabProse(match string) bool {
	body := match
	for _, prefix := range []string{"sk-ant-", "sk-", "glpat-", "xox"} {
		if rest, ok := strings.CutPrefix(match, prefix); ok {
			body = rest
			break
		}
	}
	return kebabProseRe.MatchString(body)
}
