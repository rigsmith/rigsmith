package compatibility

import (
	"strings"
	"testing"
)

// The comparison must not hide changes to user data or newly added metadata.
func TestNormalizationPreservesBehavioralDifferences(t *testing.T) {
	s := &sandbox{t: t, root: t.TempDir()}
	a := []byte(`{"seen":"2026-01-02T03:04:05Z","account":"first","extra":{"seen":"2026-01-02T03:04:05Z"}}`)
	b := []byte(strings.Replace(string(a), "2026-01-02T03:04:05Z", "2026-02-03T04:05:06Z", 1))
	const ledger = "home/.clauderig/repo/index/fixture.jsonl"
	if s.normalized(ledger, a) != s.normalized(ledger, b) {
		t.Fatal("generated ledger time was not normalized")
	}
	for _, path := range []string{"home/.claude/projects/p/s.jsonl", "home/.clauderig/repo/cli/settings.json"} {
		if s.normalized(path, a) == s.normalized(path, b) {
			t.Fatalf("user data difference hidden in %s", path)
		}
	}
	for _, changed := range []string{
		strings.Replace(string(a), "first", "second", 1),
		strings.ReplaceAll(string(a), "2026-01-02T03:04:05Z", "2026-02-03T04:05:06Z"),
		strings.Replace(string(a), `"seen":"2026-01-02T03:04:05Z",`, "", 1),
	} {
		if s.normalized(ledger, a) == s.normalized(ledger, []byte(changed)) {
			t.Fatal("attribution, unknown metadata, or a missing field was hidden")
		}
	}
}
