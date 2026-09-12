package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// line builds one rollout envelope. A builder rather than literals because the
// envelope and its payload have to agree about the stream type, and a literal
// that drifts on one side passes against a bug.
func line(t *testing.T, at, typ string, payload any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	env, err := json.Marshal(Envelope{Timestamp: at, Type: typ, Payload: b})
	if err != nil {
		t.Fatal(err)
	}
	return string(env)
}

func writeRollout(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleName = "rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"
const sampleID = "01a0722a-7356-7592-922a-336289bdc101"

func TestReadMetaReadsTheHeaderRecord(t *testing.T) {
	p := writeRollout(t, t.TempDir(), sampleName,
		line(t, "2026-09-05T11:22:59.000Z", TypeSessionMeta, map[string]any{
			"session_id": sampleID, "id": sampleID,
			"timestamp":  "2026-09-05T11:22:59.016Z",
			"cwd":        "/Users/someone/Git/thing",
			"originator": "vscode", "cli_version": "0.144.6", "source": "vscode",
			"model_provider": "openai",
			"git":            map[string]any{"branch": "feat/x", "commit_hash": "abc123", "repository_url": "git@example.test:o/r.git"},
		}),
		line(t, "2026-09-05T11:23:00.000Z", TypeResponseItem, map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hello"}}}),
	)
	m, ok, err := ReadMeta(p)
	if err != nil || !ok {
		t.Fatalf("ReadMeta ok=%v err=%v", ok, err)
	}
	if m.SessionID != sampleID || m.Cwd != "/Users/someone/Git/thing" || m.Branch != "feat/x" || m.CLIVersion != "0.144.6" {
		t.Fatalf("meta = %+v", m)
	}
	if m.At.IsZero() {
		t.Error("the header's own timestamp should have been parsed")
	}
}

func TestADetachedHeadIsReportedAsNoBranch(t *testing.T) {
	p := writeRollout(t, t.TempDir(), sampleName,
		line(t, "2026-09-05T11:22:59.000Z", TypeSessionMeta, map[string]any{
			"session_id": sampleID, "git": map[string]any{"branch": "HEAD"},
		}))
	m, _, _ := ReadMeta(p)
	if m.Branch != "" {
		// "HEAD" is a branch name nobody typed; showing it in a listing is
		// worse than showing none.
		t.Errorf("Branch = %q, want empty for a detached head", m.Branch)
	}
}

func TestFirstPromptSkipsWhatCodexInjects(t *testing.T) {
	// Codex opens a session with the sandbox rules and the project's AGENTS.md
	// as messages. A title taken from those reads identically for every session
	// in a repo.
	p := writeRollout(t, t.TempDir(), sampleName,
		line(t, "2026-09-05T11:22:59Z", TypeSessionMeta, map[string]any{"session_id": sampleID}),
		line(t, "2026-09-05T11:23:00Z", TypeResponseItem, map[string]any{"type": "message", "role": "developer",
			"content": []any{map[string]any{"type": "input_text", "text": "<permissions instructions>\nFilesystem sandboxing…"}}}),
		line(t, "2026-09-05T11:23:01Z", TypeResponseItem, map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /repo\n<INSTRUCTIONS>…"}}}),
		line(t, "2026-09-05T11:23:02Z", TypeResponseItem, map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "Please review the launcher\n"}}}),
	)
	if got := FirstPrompt(p); got != "Please review the launcher" {
		t.Errorf("FirstPrompt = %q, want the first thing the person actually typed", got)
	}
}

func TestTextIsReadFromBothStreams(t *testing.T) {
	// The single most error-prone thing about this format: the same message can
	// arrive as a response_item or as an event_msg, and a reader that handles
	// one misses sessions written the other way.
	asItem := line(t, "2026-09-05T11:23:00Z", TypeResponseItem, map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "from the item stream"}}})
	asEvent := line(t, "2026-09-05T11:23:01Z", TypeEventMsg, map[string]any{"type": "user_message", "message": "from the event stream"})
	reply := line(t, "2026-09-05T11:23:02Z", TypeEventMsg, map[string]any{"type": "agent_message", "message": "the answer"})

	for _, tc := range []struct{ line, role, text string }{
		{asItem, RoleUser, "from the item stream"},
		{asEvent, RoleUser, "from the event stream"},
		{reply, RoleAssistant, "the answer"},
	} {
		role, text, ok := MessageText(tc.line)
		if !ok || role != tc.role || text != tc.text {
			t.Errorf("MessageText = (%q,%q,%v), want (%q,%q,true)", role, text, ok, tc.role, tc.text)
		}
	}
}

func TestNonProseContentIsNotTurnedIntoText(t *testing.T) {
	l := line(t, "2026-09-05T11:23:00Z", TypeResponseItem, map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_image", "image_url": "data:…"}}})
	if _, text, ok := MessageText(l); ok && strings.Contains(text, "input_image") {
		t.Error("an image's type leaked into the text; a listing would show \"input_image\"")
	}
}

func TestLastActivityTakesTheNewestRecordNotTheLastLine(t *testing.T) {
	// Codex interleaves streams, and the final lines of a rollout are usually
	// token accounting rather than anything said.
	p := writeRollout(t, t.TempDir(), sampleName,
		line(t, "2026-09-05T11:22:59Z", TypeSessionMeta, map[string]any{"session_id": sampleID}),
		line(t, "2026-09-05T12:00:00Z", TypeEventMsg, map[string]any{"type": "user_message", "message": "the last thing I asked"}),
		line(t, "2026-09-05T12:00:05Z", TypeTurnContext, map[string]any{"model": "gpt-6-astra"}),
		line(t, "2026-09-05T11:30:00Z", TypeEventMsg, map[string]any{"type": "token_count"}), // older, out of order
	)
	a, ok := LastActivity(p)
	if !ok {
		t.Fatal("LastActivity found nothing")
	}
	want, _ := time.Parse(time.RFC3339, "2026-09-05T12:00:05Z")
	if !a.At.Equal(want) {
		t.Errorf("At = %v, want the newest timestamp %v", a.At, want)
	}
	if a.LastPrompt != "the last thing I asked" {
		t.Errorf("LastPrompt = %q", a.LastPrompt)
	}
	if a.Model != "gpt-6-astra" {
		t.Errorf("Model = %q", a.Model)
	}
}

func TestIsConversationLineKeepsWhatItCannotParse(t *testing.T) {
	// A search that silently skips an unreadable line reports "no such
	// conversation" for a session it merely failed to read.
	if !IsConversationLine("{not json") {
		t.Error("an unparseable line must not be hidden from a search")
	}
	if IsConversationLine(line(t, "2026-09-05T11:23:00Z", TypeTokenUsage, map[string]any{})) {
		t.Error("token accounting must not match a search; every session would hit")
	}
	if !IsConversationLine(line(t, "2026-09-05T11:23:00Z", TypeResponseItem, map[string]any{"type": "message"})) {
		t.Error("a message must be searchable")
	}
}

func TestIsRolloutRelAcceptsOnlyTheRealShape(t *testing.T) {
	ok := []string{
		"sessions/2026/09/05/" + sampleName,
		"archived_sessions/" + sampleName,
	}
	for _, rel := range ok {
		if !IsRolloutRel(rel) {
			t.Errorf("IsRolloutRel(%q) = false, want true", rel)
		}
	}
	bad := []string{
		"sessions/2026/09/05/notes.jsonl",       // right place, wrong name
		"skills/x/" + sampleName,                // right name, wrong place
		"sessions/2026/09/" + sampleName,        // shard is incomplete
		"sessions/2026/09/05/sub/" + sampleName, // a level too deep
		"session_index.jsonl",
	}
	for _, rel := range bad {
		if IsRolloutRel(rel) {
			t.Errorf("IsRolloutRel(%q) = true, want false", rel)
		}
	}
}

func TestIDComesFromTheFilenameWithoutOpeningTheFile(t *testing.T) {
	if got := IDFromRolloutRel("sessions/2026/09/05/" + sampleName); got != sampleID {
		t.Errorf("IDFromRolloutRel = %q, want %q", got, sampleID)
	}
	if got := IDFromRolloutRel("sessions/2026/09/05/notes.jsonl"); got != "" {
		t.Errorf("IDFromRolloutRel invented %q for a non-rollout", got)
	}
}

func TestDateOfReadsTheShard(t *testing.T) {
	got, ok := DateOf("sessions/2026/09/05/" + sampleName)
	if !ok {
		t.Fatal("DateOf found no shard")
	}
	if got.Format("2006-01-02") != "2026-09-05" {
		t.Errorf("DateOf = %v", got)
	}
	if _, ok := DateOf("archived_sessions/" + sampleName); ok {
		t.Error("the flat archive has no shard to read")
	}
}

func TestLastUsageReadsTheNewestTokenCount(t *testing.T) {
	p := writeRollout(t, t.TempDir(), sampleName,
		line(t, "2026-09-05T11:22:59Z", TypeSessionMeta, map[string]any{"session_id": sampleID}),
		line(t, "2026-09-05T11:30:00Z", TypeEventMsg, map[string]any{"type": "token_count",
			"info": map[string]any{"model_context_window": 258400,
				"total_token_usage": map[string]any{"input_tokens": 100, "cached_input_tokens": 10, "output_tokens": 20, "reasoning_output_tokens": 5, "total_tokens": 120}}}),
		line(t, "2026-09-05T11:40:00Z", TypeEventMsg, map[string]any{"type": "token_count",
			"info": map[string]any{"model_context_window": 258400,
				"total_token_usage": map[string]any{"input_tokens": 900, "cached_input_tokens": 80, "output_tokens": 70, "reasoning_output_tokens": 9, "total_tokens": 970}}}),
	)
	u, ok := LastUsage(p)
	if !ok {
		t.Fatal("LastUsage found nothing")
	}
	if u.Total != 970 || u.Input != 900 || u.ContextWindow != 258400 {
		t.Errorf("usage = %+v, want the newest accounting", u)
	}
}

func TestTidyCollapsesAndTruncates(t *testing.T) {
	got := Tidy("a  line\nwith\tbreaks")
	if got != "a line with breaks" {
		t.Errorf("Tidy = %q", got)
	}
	long := strings.Repeat("x", 200)
	if out := Tidy(long); len([]rune(out)) != promptMax+1 || !strings.HasSuffix(out, "…") {
		t.Errorf("Tidy did not truncate to %d runes plus an ellipsis: %d runes", promptMax, len([]rune(out)))
	}
}
