package sessions

import (
	"strings"
	"testing"
)

func promptLine(text, ts string) string {
	return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
}

// The two ends of a conversation are what identify it: what you opened it to do
// and what you last asked.
func TestPrompts_FirstAndLast(t *testing.T) {
	var b strings.Builder
	for _, p := range []string{"one", "two", "three", "four", "five", "six"} {
		b.WriteString(promptLine(p, "2026-08-20T09:00:00Z"))
	}
	c, err := Prompts(write(t, t.TempDir(), "s.jsonl", b.String()), 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 6 {
		t.Errorf("Total = %d, want 6", c.Total)
	}
	if len(c.First) != 2 || c.First[0].Text != "one" || c.First[1].Text != "two" {
		t.Errorf("First = %+v", c.First)
	}
	if len(c.Last) != 2 || c.Last[0].Text != "five" || c.Last[1].Text != "six" {
		t.Errorf("Last = %+v", c.Last)
	}
	if c.First[0].At.IsZero() {
		t.Error("prompt timestamps were dropped")
	}
}

// A short conversation's two ends are the same prompts. Repeating them would
// render as a session that asked everything twice.
func TestPrompts_ShortSessionDoesNotRepeatItself(t *testing.T) {
	body := promptLine("only", "2026-08-20T09:00:00Z") + promptLine("second", "2026-08-20T09:01:00Z")
	c, _ := Prompts(write(t, t.TempDir(), "s.jsonl", body), 3)
	if len(c.First) != 2 {
		t.Errorf("First = %+v, want both", c.First)
	}
	if len(c.Last) != 0 {
		t.Errorf("Last = %+v, want empty when everything is already in First", c.Last)
	}
}

// Same predicate as the title, so plumbing never poses as something you said.
func TestPrompts_ExcludesPlumbing(t *testing.T) {
	body := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"out"}]}}` + "\n" +
		promptLine("<ide_opened_file>/tmp/x</ide_opened_file>", "2026-08-20T09:00:00Z") +
		promptLine("Caveat: this session was resumed", "2026-08-20T09:00:30Z") +
		promptLine("the real question", "2026-08-20T09:01:00Z") +
		`{"type":"assistant","message":{"role":"assistant","content":"answer"}}` + "\n"
	c, _ := Prompts(write(t, t.TempDir(), "s.jsonl", body), 5)
	if c.Total != 1 || len(c.First) != 1 || c.First[0].Text != "the real question" {
		t.Errorf("got %+v, want only the typed prompt", c)
	}
}

func TestPrompts_MissingFile(t *testing.T) {
	if _, err := Prompts("/nope/missing.jsonl", 2); err == nil {
		t.Error("a missing transcript should error rather than read as empty")
	}
}
