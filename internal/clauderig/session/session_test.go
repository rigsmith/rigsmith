package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeSidecar(t *testing.T, base, org, user, id, body string) {
	t.Helper()
	dir := filepath.Join(base, "claude-code-sessions", org, user)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local_"+id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_IndexesByCliSessionIdAndMergesSources(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	org, user := "org1", "user1"

	// Same session in both roots; repo copy is older → live supplies display fields.
	writeSidecar(t, live, org, user, "aaa",
		`{"cliSessionId":"cli-aaa","title":"Billing pipeline","cwd":"/Users/j/Git","model":"claude-opus-4-8","lastActivityAt":2000,"isArchived":false}`)
	writeSidecar(t, repo, org, user, "aaa",
		`{"cliSessionId":"cli-aaa","title":"Billing pipeline (old)","cwd":"$HOME/Git","lastActivityAt":1000}`)
	// A second session only in repo.
	writeSidecar(t, repo, org, user, "bbb",
		`{"cliSessionId":"cli-bbb","title":"Other","lastActivityAt":500}`)
	// A placeholder with no cliSessionId — must be ignored.
	writeSidecar(t, live, org, user, "ccc", `{"title":"Local sessions storage"}`)

	idx := Build([]Root{{Label: "desktop", Base: live}, {Label: "repo", Base: repo}})

	if len(idx) != 2 {
		t.Fatalf("index has %d entries, want 2 (cli-aaa, cli-bbb): %v", len(idx), idx)
	}
	a := idx["cli-aaa"]
	if a.Title != "Billing pipeline" {
		t.Errorf("newer sidecar should win title, got %q", a.Title)
	}
	if len(a.Sources) != 2 {
		t.Errorf("cli-aaa should record both sources, got %v", a.Sources)
	}
	if a.LastActivity.IsZero() {
		t.Errorf("lastActivity should be parsed")
	}
	if _, ok := idx["cli-ccc"]; ok {
		t.Errorf("placeholder without cliSessionId should be skipped")
	}
}

// Cowork/agent sessions store their sidecar in local-agent-mode-sessions, beside
// a local_<id>/ working dir. Build must index that tree too (so the human title
// surfaces) and must not descend into the working dir's outputs/.claude subtree.
func TestBuild_IndexesCoworkTreeAndSkipsWorkingDir(t *testing.T) {
	base := t.TempDir()
	acct, org := "acct1", "org1"
	dir := filepath.Join(base, "local-agent-mode-sessions", acct, org)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The cowork sidecar carrying the human title.
	if err := os.WriteFile(filepath.Join(dir, "local_cow.json"),
		[]byte(`{"cliSessionId":"cli-cow","title":"Quarterly expense report","cwd":"/x/outputs","lastActivityAt":3000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A decoy local_*.json buried inside the session's working dir — must NOT be
	// indexed (Build skips descending into local_<id>/).
	work := filepath.Join(dir, "local_cow", "outputs", ".claude", "projects", "slug")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "local_decoy.json"),
		[]byte(`{"cliSessionId":"cli-decoy","title":"should not be indexed"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := Build([]Root{{Label: "desktop", Base: base}})

	m, ok := idx["cli-cow"]
	if !ok || m.Title != "Quarterly expense report" {
		t.Fatalf("cowork sidecar not indexed with its title: %+v", idx)
	}
	if _, ok := idx["cli-decoy"]; ok {
		t.Errorf("Build descended into the session working dir and indexed a decoy sidecar")
	}
}

func TestBuild_MissingTreeIsNoError(t *testing.T) {
	idx := Build([]Root{{Label: "x", Base: "/no/such/base"}})
	if len(idx) != 0 {
		t.Fatalf("missing base should yield empty index, got %v", idx)
	}
}

func TestIDFromTranscriptRel(t *testing.T) {
	cases := map[string]string{
		"projects/-slug-git/sess-aaa.jsonl":                                 "sess-aaa",
		"projects/-slug/sess-id/subagents/agent-x.jsonl":                    "sess-id",
		"cli/projects/-slug-git/abc-123.jsonl":                              "abc-123",
		"settings.json":                                                     "",
		"file-history/abc/edit@v1":                                          "",
		"desktop/local-agent-mode-sessions/x/.claude/projects/-s/zzz.jsonl": "zzz",
	}
	for rel, want := range cases {
		if got := IDFromTranscriptRel(rel); got != want {
			t.Errorf("IDFromTranscriptRel(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestIsConversationLine(t *testing.T) {
	conv := []string{
		`{"type":"user","message":{"role":"user","content":"do the billing run"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`not json at all`, // unparseable — kept so we never hide a real hit
	}
	noise := []string{
		`{"type":"attachment","attachment":{"type":"skill_listing","content":"- anthropic-skills:some-skill"}}`,
		`{"isSidechain":false,"attachment":{"type":"skill_listing"}}`, // no type field, but has attachment
		`{"type":"system","content":"system note mentioning a topic"}`,
		`{"type":"queue-operation","content":"a topic"}`,
	}
	for _, l := range conv {
		if !IsConversationLine(l) {
			t.Errorf("IsConversationLine(%.40q) = false, want true", l)
		}
	}
	for _, l := range noise {
		if IsConversationLine(l) {
			t.Errorf("IsConversationLine(%.40q) = true, want false", l)
		}
	}
}

func TestFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	// First user record is a DOM probe (skip), then a real prompt (take).
	body := `{"type":"user","message":{"role":"user","content":"<!-- DOM Probe -->"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"make the billing summary for May"}}` + "\n"
	p := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FirstPrompt(p); got != "make the billing summary for May" {
		t.Errorf("FirstPrompt = %q, want the real prompt", got)
	}

	// Array-form content is also extracted.
	p2 := filepath.Join(dir, "t2.jsonl")
	os.WriteFile(p2, []byte(`{"type":"user","message":{"content":[{"type":"text","text":"array form question"}]}}`+"\n"), 0o644)
	if got := FirstPrompt(p2); got != "array form question" {
		t.Errorf("FirstPrompt(array) = %q", got)
	}

	// No usable prompt → empty.
	p3 := filepath.Join(dir, "t3.jsonl")
	os.WriteFile(p3, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"only assistant"}]}}`+"\n"), 0o644)
	if got := FirstPrompt(p3); got != "" {
		t.Errorf("FirstPrompt(no user) = %q, want empty", got)
	}

	// A long multibyte prompt must truncate on a rune boundary (valid UTF-8), not
	// mid-character.
	p4 := filepath.Join(dir, "t4.jsonl")
	long := strings.Repeat("é", 100) // 100 runes, 200 bytes — crosses the 70-rune cap
	os.WriteFile(p4, []byte(`{"type":"user","message":{"content":"`+long+`"}}`+"\n"), 0o644)
	got := FirstPrompt(p4)
	if !utf8.ValidString(got) {
		t.Errorf("FirstPrompt truncated mid-rune (invalid UTF-8): %q", got)
	}
	if r := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); r != 70 {
		t.Errorf("truncated to %d runes, want 70 + ellipsis", r)
	}
}

// Session ids are uuids, and uuids are case-insensitive by specification.
// Claude Code writes transcript filenames lowercase; a Desktop sidecar carries
// whatever `cliSessionId` it was handed. Keyed by the raw value, an uppercase
// sidecar never matched the transcript beside it and the session lost its title
// and project everywhere the index is consulted.
func TestBuild_KeysAreCaseCanonical(t *testing.T) {
	live := t.TempDir()
	up := "ABCDEF01-2345-4678-89AB-CDEF01234567"
	writeSidecar(t, live, "org1", "user1", "up",
		`{"cliSessionId":"`+up+`","title":"Uppercase sidecar","lastActivityAt":3000}`)

	idx := Build([]Root{{Label: "desktop", Base: live}})
	m, ok := idx[CanonicalID(up)]
	if !ok {
		t.Fatalf("lowercase lookup missed an uppercase sidecar; keys = %v", keysOf(idx))
	}
	if m.Title != "Uppercase sidecar" {
		t.Errorf("title = %q, want the sidecar's", m.Title)
	}
	if m.ID != CanonicalID(up) {
		t.Errorf("Meta.ID = %q, want the canonical form", m.ID)
	}
}

func keysOf(idx Index) []string {
	out := make([]string, 0, len(idx))
	for k := range idx {
		out = append(out, k)
	}
	return out
}

// Real transcripts bury the first typed prompt behind a thick preamble —
// queue-operations, IDE-state records, attachments, file-history snapshots —
// and behind tool results, which Claude Code also records as "user" records.
// A scan bounded by raw lines, or one that counts tool results as candidates,
// misses the prompt entirely and the session lists as untitled.
func TestFirstPromptPastAThickPreamble(t *testing.T) {
	var b strings.Builder
	// Preamble: the kinds of record that sit in front of a real prompt.
	for range 40 {
		b.WriteString(`{"type":"queue-operation"}` + "\n")
		b.WriteString(`{"type":"attachment","attachment":{"x":1}}` + "\n")
		b.WriteString(`{"type":"file-history-snapshot"}` + "\n")
	}
	// An IDE-state record, which looks like a user message but is skipped.
	b.WriteString(`{"type":"user","message":{"role":"user","content":"<ide_opened_file>/tmp/x</ide_opened_file>"}}` + "\n")
	// Tool results — recorded as user records, but carrying no typed text.
	for range 20 {
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"out"}]}}` + "\n")
	}
	// Finally, the thing the human actually typed.
	b.WriteString(`{"type":"user","message":{"role":"user","content":"Lets try option #1"}}` + "\n")

	if got := FirstPromptFrom(strings.NewReader(b.String())); got != "Lets try option #1" {
		t.Fatalf("FirstPromptFrom = %q, want the typed prompt", got)
	}
}

// A session with no typed human message anywhere has no title, and that is the
// correct answer rather than a failure — IDE-opened-file and tool-driven
// sessions genuinely contain nothing a person wrote.
func TestFirstPromptEmptyWhenNothingWasTyped(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"<ide_opened_file>/tmp/x</ide_opened_file>"}}` + "\n")
	for range 30 {
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"out"}]}}` + "\n")
		b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"working"}}` + "\n")
	}
	if got := FirstPromptFrom(strings.NewReader(b.String())); got != "" {
		t.Fatalf("FirstPromptFrom = %q, want empty", got)
	}
}

// The scan stays bounded: a transcript body runs to megabytes and must never be
// parsed whole just to find a title.
func TestFirstPromptStaysBounded(t *testing.T) {
	var b strings.Builder
	for range maxScanLines + 500 {
		b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"filler"}}` + "\n")
	}
	b.WriteString(`{"type":"user","message":{"role":"user","content":"far too deep to count"}}` + "\n")
	if got := FirstPromptFrom(strings.NewReader(b.String())); got != "" {
		t.Fatalf("scan ran past its line budget: %q", got)
	}
}
