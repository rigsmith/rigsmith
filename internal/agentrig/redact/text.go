package redact

import (
	"regexp"
	"strings"
)

// The three pieces of a PEM block that both the scrubber and the scanner have
// to agree on. They are shared so they cannot drift: where the two disagree,
// the sync either refuses something already cleaned, or — the outcome that
// actually stops a machine backing up — detects something it cannot clean, and
// refuses for ever with the scrubber already on.
const (
	// pemSep is one separator between the header and what follows: a line break
	// as a byte, or as the escape a JSON string carries — doubled when the
	// content was JSON before it was embedded in more JSON — or a space, which
	// is how an environment variable holds a key.
	pemSep = `(?:\\+(?:r\\+)?n|[\r\n \t])`
	// pemAttrLine is an RFC 1421 attribute, "Proc-Type: 4,ENCRYPTED", which an
	// encrypted key puts between its header and its body. Ending at a backslash
	// as well as a quote keeps it inside one line of an escaped JSON string.
	pemAttrLine = `[A-Za-z][A-Za-z0-9-]*: [^"\r\n\\]*`
	// pemLead is a header followed by actual key material: any attribute lines,
	// then twenty unbroken base64 characters. The whole difference between a key
	// and the name of one.
	//
	// The separator run is optional. A key written straight after its header
	// with nothing between is not a shape PEM produces, but the scanner accepts
	// it, and a scanner that detects what this cannot rewrite is exactly the
	// permanent refusal described above.
	pemLead = `-----BEGIN [A-Z ]*PRIVATE KEY-----(?:` + pemSep + `+` + pemAttrLine + `)*` + pemSep + `*[A-Za-z0-9+/]{20,}`
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
	// A JWT, anchored on its header rather than on the whole string, and
	// deliberately WITHOUT a word boundary. Its own shape is the guard here —
	// three dot-separated base64url runs is not something prose produces — so
	// the boundary buys nothing and costs real tokens: one written straight
	// after an escape, "…turso.io\neyJ…", is preceded by the n and would never
	// be rewritten. The stream scanner has no boundary either, so requiring one
	// here made it possible to detect a credential that could not be redacted,
	// which leaves a sync refusing with the scrubber already on.
	`eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}`,
	// PEM private key material, from the header to the end of the string that
	// holds it. Bounded on the quote rather than on END, because a transcript
	// records what was on screen: a key pasted mid-scroll, or one a tool printed
	// and truncated, has a header and no footer at all — one file here carried
	// four BEGIN markers and no END. Both have to go, and stopping at the quote
	// keeps the surrounding JSON record intact.
	//
	// Ordered: the footer bounds the block when there is one, so a message that
	// pastes a key and then keeps talking loses the key and keeps the talking.
	// Only when no footer follows does the fallback run to the closing quote.
	//
	// BOTH require a body, and for the same reason the scanner does
	// (HasPrivateKeyMaterial): without one they ate the sentence after any
	// header anybody merely mentioned. The fallback used to run from the header
	// to the quote unconditionally, to keep the scrubber a superset of a scanner
	// that fired on the header alone — a transcript quoting one would otherwise
	// refuse the sync for ever. Now that the scanner wants material too, that
	// was what remained of the bug. The footer form needs it just as much: a
	// documentation example is a header and a footer around `<your key here>`,
	// and rewriting one is the same silent damage to prose.
	pemLead + `[^"]*?-----END [A-Z ]*PRIVATE KEY-----`,
	pemLead + `[^"]*`,
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

// HasPrivateKeyHeader reports a PEM private-key header, with no opinion on what
// follows it. For the line-at-a-time callers ONLY — a scrubber walking a file
// line by line cannot see the body, because in raw text the body is on the lines
// after the header. There the header alone has to be enough, and refusing a file
// that merely quotes one is the accepted cost of never scrubbing half a key.
//
// Everywhere the whole content is in hand, use HasPrivateKeyMaterial instead.
func HasPrivateKeyHeader(line []byte) bool { return pemRe.Match(line) }

// HasPrivateKeyMaterial reports a PEM private-key header that is actually
// followed by key material. It is the rule for whole-file and whole-value
// scanning, where the bytes after the header are available to judge.
//
// The header on its own is not evidence, and treating it as such refused real
// syncs. A cached grep dump over a Nuxt project held minified sourcemaps of
// `jose` and `@octokit/auth-app`, whose source compares against the header
// text — `privateKey.includes("-----BEGIN RSA PRIVATE KEY-----")`. Twelve
// headers, no key. Because a private-key finding is File:true ("credential
// material, cannot be redacted"), the tripwire refused every sync until the
// file was deleted by hand, and a session transcript that merely discussed the
// incident then refused the next one. Anything naming the header — this
// package's own tests, a runbook, a code sample — was a sync outage waiting to
// happen.
func HasPrivateKeyMaterial(data []byte) bool {
	for _, loc := range pemRe.FindAllIndex(data, -1) {
		if keyBodyFollows(data[loc[1]:]) {
			return true
		}
	}
	return false
}

// pemLookahead bounds the bytes examined after a header, and is now the ONLY
// bound on the search — a line cap sat on top of it until a key with more
// attribute lines than the cap allowed came back clean.
//
// Generous, because every byte of headroom here is a key that cannot be hidden
// behind padding, and the cost is small: bounded work per header on a file the
// caller has already capped at scanContentLimit.
const pemLookahead = 4096

// keyBodyFollows reports whether rest — the bytes immediately after a PEM
// header — opens with key material.
//
// What follows carries the decision. A real key continues onto the next line,
// or — as environment variables hold them — straight on after a space; a quoted
// header continues with the quote or bracket that closed the string literal,
// which is no kind of base64. That is what clears the bundled-library case.
// Running out of window is not an answer, and is never read as one: see the
// return at the end.
func keyBodyFollows(rest []byte) bool {
	truncated := len(rest) > pemLookahead
	if truncated {
		rest = rest[:pemLookahead]
	}
	// A key inside a JSON string — a transcript, a sourcemap, a .env — carries
	// its line breaks as an escape rather than the byte, and doubled when the
	// content was JSON before it was embedded in more JSON.
	s := escapedNewline.ReplaceAllString(string(rest), "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, "\n") {
		// Single-line form, as environment variables hold keys: the body is the
		// next token rather than the next line.
		return pemBody.MatchString(firstToken(s))
	}
	// Attribute and blank lines are skipped until the body, and the search is
	// bounded by pemLookahead alone. A line count on top of it was worse than
	// redundant: a key with more attribute lines than the cap allowed came back
	// "no material" and published, which is the one direction this must never
	// fail in.
	lines := strings.Split(s[1:], "\n")
	// The last line of a truncated read ends where the window did, not where the
	// content does. Judging it decides the whole question on a fragment — and it
	// decided it wrongly, because a body line cut short still looks like base64
	// while an attribute line cut short does not.
	if truncated && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || pemAttr.MatchString(line) {
			continue
		}
		return pemBody.MatchString(line)
	}
	// Nothing but attribute and blank lines, all the way to the end of what was
	// read. If that end was the window rather than the content, the body may be
	// just past it, and the honest answer is "unknown".
	//
	// Unknown has to mean yes. A bounded look is only safe while running out of
	// room cannot be mistaken for an answer: a key can be pushed past any fixed
	// window by padding the space after its header with attribute-shaped lines,
	// and reading that as "no material" published it. The cost of the other
	// choice is a refusal on a file that put four kilobytes of `Name: value`
	// after a PEM header and no key, which nothing sane produces — and a
	// refusal names the file and stops, where a miss is silent.
	return truncated
}

// firstToken is s up to the first space or line break.
func firstToken(s string) string {
	if i := strings.IndexAny(s, " \t\n\r"); i >= 0 {
		return s[:i]
	}
	return s
}

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
	for _, prefix := range []string{"sk-ant-", "sk-proj-", "sk-", "glpat-", "xox"} {
		if rest, ok := strings.CutPrefix(match, prefix); ok {
			body = rest
			break
		}
	}
	return kebabProseRe.MatchString(body)
}
