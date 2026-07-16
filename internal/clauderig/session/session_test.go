package session

import (
	"os"
	"path/filepath"
	"testing"
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
}
