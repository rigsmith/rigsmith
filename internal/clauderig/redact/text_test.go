package redact

import (
	"bytes"
	"encoding/json"
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
		// Shaped like the real credential, not merely long enough. An AWS access
		// key id is exactly twenty characters, and the text rule now says so —
		// open-ended, it matched any shouted phrase containing those four
		// letters. A fixture that is unrealistic in that way would force the
		// rule to stay loose to satisfy it.
		body := strings.Repeat("A", 24)
		if p.prefix == "AKIA" || p.prefix == "ASIA" {
			body = strings.Repeat("A", 16)
		}
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

// LooksSecret calls an opaque bearer token a credential when it judges a config
// value; a transcript must not be the one place it survives.
func TestRedactText_BearerToken(t *testing.T) {
	in := []byte("curl -H 'Authorization: Bearer 8xLOxBtZp8kFqz5mNvQ2wRt7yHjKlPoI'\n")
	out, hits, changed := RedactText(in)
	if !changed {
		t.Fatal("a bearer token was left in the transcript")
	}
	if strings.Contains(string(out), "8xLOxBtZp8kFqz5mNvQ2wRt7yHjKlPoI") {
		t.Errorf("the token survived: %s", out)
	}
	if len(hits) != 1 || hits[0].Kind != "bearer" {
		t.Errorf("hits = %+v, want one bearer", hits)
	}
}

// `Bearer` appears in every API example ever pasted into a chat. Rewriting the
// middle of somebody's documentation is the cost of guessing wrong here, and
// the original is not kept.
func TestRedactText_LeavesBearerPlaceholders(t *testing.T) {
	for _, s := range []string{
		"Authorization: Bearer YOUR_ACCESS_TOKEN_GOES_HERE",
		"Authorization: Bearer $ANTHROPIC_API_KEY",
		"Authorization: Bearer <your-token>",
		"Authorization: Bearer abc123",
	} {
		if out, _, changed := RedactText([]byte(s)); changed {
			t.Errorf("rewrote an example: %q became %q", s, out)
		}
	}
}

// The rules run over conversation prose, so they must not fire inside ordinary
// words. `sk-` is the tail of "task-", "risk-" and "desk-"; AKIA and ASIA sit
// inside longer uppercase and base64 runs. On one real machine this was 96 of
// 134 findings, and every one of them refused a sync that should have run.
func TestRedactText_DoesNotMatchInsideWords(t *testing.T) {
	for _, s := range []string{
		"the global-task-runner-configuration is fine",
		"a risk-assessment-matrix-worksheet",
		"see the desk-allocation-spreadsheet-2026",
		"deployed to ASIAPACIFICREGIONSETTINGS today",
		"the token KA2AwiASIAQQQQQQQQQQQQQQQQ was rotated",
	} {
		if out, hits, changed := RedactText([]byte(s)); changed {
			t.Errorf("rewrote ordinary text %q → %q (%+v)", s, out, hits)
		}
	}
}

// Anchored, but the prefixes still begin real words. A conversation about this
// very tool is full of hyphenated lowercase phrases.
func TestRedactText_LeavesHyphenatedProse(t *testing.T) {
	for _, s := range []string{
		"sk-a-single-line-of-explanation",
		"sk-the-window-has-no-repository",
		"glpat-a-name-someone-chose",
	} {
		if out, _, changed := RedactText([]byte(s)); changed {
			t.Errorf("rewrote a phrase %q → %q", s, out)
		}
	}
}

// And none of that may cost a real credential.
func TestRedactText_StillCatchesRealShapes(t *testing.T) {
	for _, s := range []string{
		"key sk-ant-api03-" + strings.Repeat("Aa1", 20),
		"AKIA" + strings.Repeat("A", 16),
		"ghp_" + strings.Repeat("a1", 18),
		"Authorization: Bearer 8xLOxBtZp8kFqz5mNvQ2wRt7yHjKlPoI",
	} {
		if _, _, changed := RedactText([]byte(s)); !changed {
			t.Errorf("missed a real credential shape: %q", s)
		}
	}
}

// The rewriter and the tripwire have to agree about what a credential is. When
// only the rewriter learned to skip hyphenated prose, the tripwire kept
// refusing the phrases the rewriter had decided to leave alone — a sync blocked
// with the scrubber already on and nothing left for its owner to try.
func TestScanAndRedactAgreeOnWhatCountsAsACredential(t *testing.T) {
	cases := []struct {
		text   string
		secret bool
	}{
		{"sk-a-single-line-of-explanation", false},
		{"the global-task-runner-configuration", false},
		{"Authorization: Bearer YOUR_ACCESS_TOKEN_GOES_HERE", false},
		{"key sk-ant-api03-" + strings.Repeat("Aa1", 20), true},
		{"AKIA" + strings.Repeat("A", 16), true},
		{"Authorization: Bearer 8xLOxBtZp8kFqz5mNvQ2wRt7yHjKlPoI", true},
	}
	for _, tc := range cases {
		_, _, rewrote := RedactText([]byte(tc.text))
		refused := scanText("projects/-p/s.jsonl", []byte(tc.text)) != nil
		if rewrote != refused {
			t.Errorf("%q: rewriter says credential=%v, tripwire says %v — they must agree",
				tc.text, rewrote, refused)
		}
		if refused != tc.secret {
			t.Errorf("%q: treated as credential=%v, want %v", tc.text, refused, tc.secret)
		}
	}
}

// The invariant behind `redactTranscripts`: anything the tripwire can DETECT,
// the scrubber must be able to REMOVE. Where the two disagree the sync refuses
// with the scrubber already on and nothing left for its owner to try — which is
// how a PEM header nobody could clear, and a JWT written straight after an
// escape, each blocked a machine's backups indefinitely.
func TestScrubberCanRemoveEverythingTheScannerDetects(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.dBjftJeZ4CVP"
	cases := []struct{ name, body string }{
		{"jwt", `{"t":"` + jwt + `"}`},
		// The one that was blocking a real machine: no separator between the
		// escape and the token, so the header sits behind a word character.
		{"jwt after an escape", `{"t":"https://db.turso.io\n` + jwt + `"}`},
		{"pem, whole block", `{"t":"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"}`},
		{"pem, header only", `{"t":"it printed -----BEGIN OPENSSH PRIVATE KEY----- and stopped"}`},
		{"anthropic key", `{"t":"sk-ant-api03-` + strings.Repeat("Aa1", 20) + `"}`},
		{"aws key", `{"t":"AKIA` + strings.Repeat("A", 16) + `"}`},
		{"github token", `{"t":"ghp_` + strings.Repeat("a1", 18) + `"}`},
		{"bearer", `{"t":"Authorization: Bearer 8xLOxBtZp8kFqz5mNvQ2wRt7yHjKlPoI"}`},
	}
	for _, tc := range cases {
		raw := []byte(tc.body)
		if before, _ := ScanReader("projects/-p/s.jsonl", bytes.NewReader(raw)); before == nil {
			t.Errorf("%s: the scanner does not detect this at all — the case is not testing anything", tc.name)
			continue
		}
		cleaned, _, changed := RedactText(raw)
		if !changed {
			t.Errorf("%s: detected but the scrubber left it untouched — no setting can clear this", tc.name)
			continue
		}
		if after, _ := ScanReader("projects/-p/s.jsonl", bytes.NewReader(cleaned)); after != nil {
			t.Errorf("%s: still detected as %q after scrubbing — the sync would refuse for ever", tc.name, after.Kind)
		}
	}
}

// Scrubbing a PEM block must not take the rest of the record with it: the
// bound is the quote that closes the string, so the JSON survives.
func TestRedactText_PEMStopsAtTheStringItIsIn(t *testing.T) {
	in := []byte(`{"t":"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIB\n-----END RSA PRIVATE KEY-----","keep":"me"}`)
	out, _, changed := RedactText(in)
	if !changed {
		t.Fatal("the key was left in place")
	}
	if bytes.Contains(out, []byte("MIIEowIB")) {
		t.Error("key material survived")
	}
	if !bytes.Contains(out, []byte(`"keep":"me"`)) {
		t.Errorf("the rest of the record was eaten: %s", out)
	}
	var doc map[string]string
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Errorf("the scrubbed record is no longer valid JSON: %v\n%s", err, out)
	}
}

func TestReviewCredentialDecisionsAgree(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		credential  bool
	}{
		{"hyphenated prose", "sk-a-single-line-of-explanation", false},
		{"embedded prefix", "global-task-runner-config", false},
		{"project key lowercase body", "sk-proj-" + strings.Repeat("a", 48), true},
		{"project key mixed body", "sk-proj-" + strings.Repeat("Ab1", 16), true},
		{"ordinary key", "sk-" + strings.Repeat("Ab1", 16), true},
		{"anthropic key", "sk-ant-api03-" + strings.Repeat("Ab1", 16), true},
		{"placeholder", "Bearer YOUR_ACCESS_TOKEN_GOES_HERE", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "example: " + tc.token + " ends here\n"
			out, _, changed := RedactText([]byte(body))
			if changed != tc.credential {
				t.Fatalf("redaction changed=%v, want %v", changed, tc.credential)
			}
			// Both a native file and chunk payload use the complete scanner.
			for _, rel := range []string{"projects/p/s.jsonl", "projects/p/s.jsonl.chunks/000.part"} {
				finding, err := ScanReader(rel, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if (finding != nil) != tc.credential {
					t.Fatalf("%s: finding=%v, want credential=%v", rel, finding, tc.credential)
				}
				finding, err = ScanReader(rel, strings.NewReader(string(out)))
				if err != nil || finding != nil {
					t.Fatalf("%s: cleaned text refused: %v, %v", rel, finding, err)
				}
			}
		})
	}
}

func TestScanReaderBareProseAndProjectKey(t *testing.T) {
	for _, tc := range []struct {
		token      string
		credential bool
	}{
		{"sk-a-single-line-of-explanation", false},
		{"sk-proj-" + strings.Repeat("a", 48), true},
	} {
		finding, err := ScanReader("projects/p/note.txt", strings.NewReader(tc.token+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if (finding != nil) != tc.credential {
			t.Fatalf("bare-token classification: finding=%v, want credential=%v", finding, tc.credential)
		}
	}
}
