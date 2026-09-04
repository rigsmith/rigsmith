package commands

import (
	"strings"
	"testing"
)

// The SessionEnd hook hands sync its payload on stdin; the transcript it
// names is the one to flush. Anything else on stdin names nothing, and the
// caller flushes every transcript instead.
func TestHookTranscripts(t *testing.T) {
	got := hookTranscripts(strings.NewReader(`{"session_id":"s1","transcript_path":"/home/u/.claude/projects/-p/s1.jsonl","hook_event_name":"SessionEnd","reason":"exit"}`))
	if len(got) != 1 || got[0] != "/home/u/.claude/projects/-p/s1.jsonl" {
		t.Fatalf("payload: got %v", got)
	}
	for name, in := range map[string]string{
		"empty":       "",
		"not json":    "hello",
		"no path":     `{"session_id":"s1"}`,
		"blank path":  `{"transcript_path":"  "}`,
		"wrong shape": `[1,2]`,
	} {
		if got := hookTranscripts(strings.NewReader(in)); len(got) != 0 {
			t.Errorf("%s: got %v, want none", name, got)
		}
	}
}
