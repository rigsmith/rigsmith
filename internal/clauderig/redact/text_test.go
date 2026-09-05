package redact

import (
	"strings"
	"testing"
)

// A pasted key reaches a conversation in the middle of a line of prose. The
// whole-file rules in ScanFile can't see it — they require the entire file to be
// one bare token — which is why a transcript could carry a live key past the
// tripwire untouched.
func TestRedactText_FindsKeysInsideProse(t *testing.T) {
	in := []byte(`here is my key sk-ant-api03-` + strings.Repeat("a", 40) + ` please use it`)
	out, hits, changed := RedactText(in)
	if !changed {
		t.Fatal("a key in the middle of a line was not found")
	}
	if strings.Contains(string(out), "sk-ant-api03") {
		t.Errorf("the key survived: %s", out)
	}
	if !strings.Contains(string(out), Placeholder) {
		t.Errorf("no placeholder written: %s", out)
	}
	// The prose either side has to survive intact.
	if !strings.HasPrefix(string(out), "here is my key ") || !strings.HasSuffix(string(out), " please use it") {
		t.Errorf("surrounding text was damaged: %s", out)
	}
	if len(hits) != 1 || hits[0].Kind != "anthropic-key" {
		t.Errorf("hits = %+v, want one anthropic-key", hits)
	}
	if strings.Contains(hits[0].Hint, "aaaa") {
		t.Errorf("the hint reproduces the secret body: %q", hits[0].Hint)
	}
}

func TestRedactText_MultipleAndMixed(t *testing.T) {
	in := []byte("ghp_" + strings.Repeat("b", 36) + " and AKIA" + strings.Repeat("C", 16))
	out, hits, changed := RedactText(in)
	if !changed || len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2", hits)
	}
	if strings.Contains(string(out), strings.Repeat("b", 8)) ||
		strings.Contains(string(out), strings.Repeat("C", 8)) {
		t.Errorf("a secret body survived: %s", out)
	}
}

// The cost of a false positive here is rewriting the middle of somebody's
// conversation with no copy of the original kept, so the generic high-entropy
// backstop is deliberately not wired in.
func TestRedactText_LeavesOrdinaryContentAlone(t *testing.T) {
	for _, s := range []string{
		"the commit is 8c90f40a1b2c3d4e5f60718293a4b5c6d7e8f900",
		"session 2f63277b-d882-43c7-8506-1e00342eaf0d",
		"sha512-Kg8mDpJTgcXvBTQPGqBBIvIYLQPTBGFDXPFPPPPPPPPPP",
		"just some ordinary prose about API keys and tokens",
		"",
	} {
		out, hits, changed := RedactText([]byte(s))
		if changed || len(hits) != 0 {
			t.Errorf("RedactText(%q) rewrote it: %s %+v", s, out, hits)
		}
	}
}

// A short prefix with no body is a mention, not a key.
func TestRedactText_IgnoresBareMentions(t *testing.T) {
	if _, _, changed := RedactText([]byte("keys start with sk-ant- as a rule")); changed {
		t.Error("a bare prefix was treated as a key")
	}
}

// The text rules and the value rules have to describe the same world; a prefix
// added to one and not the other is a silent hole.
func TestTextRulesCoverKnownPrefixes(t *testing.T) {
	for _, p := range knownPrefixes {
		body := strings.Repeat("A", 24)
		if _, _, changed := RedactText([]byte("x " + p.prefix + body + " y")); !changed {
			t.Errorf("knownPrefixes has %q (%s) but the text rules miss it", p.prefix, p.kind)
		}
	}
}

// Re-running over already-cleaned content must be a no-op, or every sync would
// count the same redaction again.
func TestRedactText_IsIdempotent(t *testing.T) {
	once, _, _ := RedactText([]byte("key sk-ant-api03-" + strings.Repeat("a", 40)))
	if _, hits, changed := RedactText(once); changed || len(hits) != 0 {
		t.Errorf("second pass changed cleaned content: %+v", hits)
	}
}
