package commands

import (
	"io"
	"strings"
	"testing"
)

// The SessionEnd hook hands sync its payload on stdin; the transcript it
// names is the one to flush. Nothing on stdin at all names nothing, and the
// caller flushes every transcript instead. A payload that arrived but names
// no transcript is an error: flushing everything on the strength of a broken
// hook would restage every long session's transcript mid-chunk.
func TestHookTranscripts(t *testing.T) {
	got, err := hookTranscripts(strings.NewReader(`{"session_id":"s1","transcript_path":"/home/u/.claude/projects/-p/s1.jsonl","hook_event_name":"SessionEnd","reason":"exit"}`))
	if err != nil || len(got) != 1 || got[0] != "/home/u/.claude/projects/-p/s1.jsonl" {
		t.Fatalf("payload: got %v, %v", got, err)
	}
	// A hook runner that writes the payload and keeps the pipe open: the
	// path is still read, at once, without waiting for a close.
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte(`{"transcript_path":"/t/open.jsonl"}`)) }()
	defer pw.Close()
	if got, err := hookTranscripts(pr); err != nil || len(got) != 1 || got[0] != "/t/open.jsonl" {
		t.Fatalf("open pipe: got %v, %v", got, err)
	}
	// Nothing to read is not a payload: /dev/null, or a runner with nothing
	// to say. That is the by-hand case, and the caller flushes everything.
	if got, err := hookTranscripts(strings.NewReader("")); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v, %v, want nothing and no error", got, err)
	}
	for name, in := range map[string]string{
		"not json":    "hello",
		"truncated":   `{"transcript_path":"/t/`,
		"no path":     `{"session_id":"s1"}`,
		"blank path":  `{"transcript_path":"  "}`,
		"wrong shape": `[1,2]`,
	} {
		if got, err := hookTranscripts(strings.NewReader(in)); err == nil || len(got) != 0 {
			t.Errorf("%s: got %v, %v, want an error and no path", name, got, err)
		}
	}
	// A pipe that stays open with nothing on it: the wait ends, and that is
	// an error too, not a flush of everything.
	silent, silentW := io.Pipe()
	defer silentW.Close()
	if got, err := hookTranscripts(silent); err == nil || len(got) != 0 {
		t.Errorf("silent pipe: got %v, %v, want an error and no path", got, err)
	}
}
