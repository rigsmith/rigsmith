package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func writeTestFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testMachine(home string) config.Machine {
	return config.Machine{Name: "test", OS: config.OSToken(), Home: home}
}

// A session present in BOTH the live root and the synced repo must be listed once,
// titled from its sidecar, its hit count deduped (not doubled), its source showing
// both copies, and a shell-quoted resume command (cwd has a space).
func TestSearchSessions_DedupTitleAndResume(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	desk := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "My Project") // a space → must be quoted
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	line := `{"type":"user","message":{"content":"do the billing run"}}` + "\n"
	writeTestFile(t, live, "projects/-slug/sess-1.jsonl", line)
	writeTestFile(t, repo, "cli/projects/-slug/sess-1.jsonl", line) // same session, synced copy
	writeTestFile(t, desk, "claude-code-sessions/o/u/local_x.json",
		`{"cliSessionId":"sess-1","title":"Billing pipeline","cwd":"`+cwd+`","lastActivityAt":2000}`)

	targets := []search.Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}
	roots := []session.Root{{Label: "desktop", Base: desk}}

	var out, errw bytes.Buffer
	if err := searchSessions(&out, &errw, testMachine(t.TempDir()), targets, roots, "billing", false); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())

	if !strings.Contains(got, "Billing pipeline") {
		t.Errorf("missing sidecar title:\n%s", got)
	}
	if !strings.Contains(got, "1 match(es)") || strings.Contains(got, "2 match(es)") {
		t.Errorf("hit count not deduped across copies:\n%s", got)
	}
	if !strings.Contains(got, "cli+repo") {
		t.Errorf("source label should show both copies (cli+repo):\n%s", got)
	}
	wantResume := "resume: cd " + shQuote(cwd) + " && claude --resume sess-1"
	if !strings.Contains(got, wantResume) {
		t.Errorf("resume line = missing %q in:\n%s", wantResume, got)
	}
	if !strings.Contains(got, "1 session(s) match") {
		t.Errorf("summary should report a single session:\n%s", got)
	}
}

// A session that matches only by its Desktop title (no body hit) is listed, but
// gets a title-only note instead of a resume command, and its source falls back to
// the sidecar location.
func TestSearchSessions_TitleOnly(t *testing.T) {
	live := t.TempDir()
	desk := t.TempDir()
	writeTestFile(t, live, "projects/-slug/sess-2.jsonl",
		`{"type":"user","message":{"content":"unrelated chatter"}}`+"\n")
	writeTestFile(t, desk, "claude-code-sessions/o/u/local_y.json",
		`{"cliSessionId":"sess-9","title":"Rocket science notes","lastActivityAt":10}`)

	var out, errw bytes.Buffer
	err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}}, []session.Root{{Label: "desktop", Base: desk}}, "rocket", false)
	if err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "Rocket science notes") || !strings.Contains(got, "title match") {
		t.Errorf("title-only session not surfaced:\n%s", got)
	}
	if !strings.Contains(got, "matched by title") {
		t.Errorf("title-only should get the title note, not a resume command:\n%s", got)
	}
	if strings.Contains(got, "claude --resume") {
		t.Errorf("title-only must not print a resume command:\n%s", got)
	}
}

// A content hit that exists only in the synced repo is not resumable here (claude
// reads ~/.claude, not the repo), so it shows a restore note, not a resume command.
func TestSearchSessions_RepoOnlyNotResumable(t *testing.T) {
	live := t.TempDir()
	repo := t.TempDir()
	writeTestFile(t, repo, "cli/projects/-slug/sess-3.jsonl",
		`{"type":"user","message":{"content":"the widget migration plan"}}`+"\n")

	var out, errw bytes.Buffer
	err := searchSessions(&out, &errw, testMachine(t.TempDir()),
		[]search.Target{{Label: "cli", Dir: live}, {Label: "repo", Dir: repo}}, nil, "widget", false)
	if err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, "synced copy only") {
		t.Errorf("repo-only hit should warn it isn't resumable here:\n%s", got)
	}
	if strings.Contains(got, "claude --resume") {
		t.Errorf("repo-only must not print a runnable resume command:\n%s", got)
	}
	if !strings.Contains(got, "repo") {
		t.Errorf("source label should be repo:\n%s", got)
	}
}

// An empty / whitespace-only search term is rejected at the command layer before
// any scanning (ExactArgs(1) alone would let it through and match every title).
func TestSearchCmd_RejectsEmptyQuery(t *testing.T) {
	cmd := NewSearchCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"   "})
	if err := cmd.Execute(); err == nil {
		t.Fatal("empty search term should error")
	}
}

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"has space":        "'has space'",
		"":                 "''",
		"a'b":              `'a'\''b'`,
		"semi;rm -rf":      "'semi;rm -rf'",
		"/Users/j/Git/app": "/Users/j/Git/app",
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatHitKey_CollapsesCopies(t *testing.T) {
	// Live, synced-repo, and Desktop layouts of the same transcript line must key
	// the same so the hit isn't counted per copy.
	live := chatHitKey("projects/-slug/sess.jsonl", 7)
	repo := chatHitKey("cli/projects/-slug/sess.jsonl", 7)
	desk := chatHitKey("local-agent-mode-sessions/a/b/local_x/outputs/.claude/projects/-slug/sess.jsonl", 7)
	if live != repo || live != desk {
		t.Errorf("keys diverge across copies: %q / %q / %q", live, repo, desk)
	}
	if chatHitKey("projects/-slug/sess.jsonl", 8) == live {
		t.Error("different line numbers must key differently")
	}
}
