package secrets

import (
	"strings"
	"testing"
)

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
		if !Contains("x " + p.prefix + body + " y") {
			t.Errorf("knownPrefixes has %q (%s) but the text rules miss it", p.prefix, p.kind)
		}
	}
}

func TestContainsDecodedScalars(t *testing.T) {
	for _, text := range []string{"sk-" + strings.Repeat("A1", 16), "prefix ghp_" + strings.Repeat("A1", 16) + " suffix", "-----BEGIN PRIVATE KEY-----", strings.Repeat("aBcDeF"+"gHiJkL"+"mNoPqR"+"sTuVwX"+"yZ0123"+"456789", 2)} {
		if !Contains(text) {
			t.Fatal("missed credential shape")
		}
	}
	for _, text := range []string{"model-name", "EXAMPLE_API_KEY", "https://example.com/v1", "global-task-runner-config", "sha256-" + strings.Repeat("a", 64)} {
		if Contains(text) {
			t.Fatalf("flagged harmless value %q", text)
		}
	}
}
