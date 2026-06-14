package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

// A transcript shaped like the real ones: a couple of operation lines with no cwd,
// then the session header line carrying cwd.
const sampleTranscript = `{"type":"queue-operation","operation":"x","sessionId":"s","timestamp":"t"}
{"type":"queue-operation","operation":"y","sessionId":"s","timestamp":"t"}
{"type":"user","cwd":"/Users/john/Git/rigsmith","gitBranch":"main","isSidechain":false,"sessionId":"s"}
{"type":"assistant","cwd":"/Users/john/Git/rigsmith","sessionId":"s"}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCwdFromTranscript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, p, sampleTranscript)
	cwd, ok, err := CwdFromTranscript(p)
	if err != nil || !ok || cwd != "/Users/john/Git/rigsmith" {
		t.Fatalf("got cwd=%q ok=%v err=%v", cwd, ok, err)
	}
}

func TestCwdFromTranscript_SkipsSidechain(t *testing.T) {
	// A sidechain (sub-agent) line with a different cwd must be skipped in favour
	// of the session's own cwd.
	content := `{"type":"user","cwd":"/Users/john/elsewhere","isSidechain":true,"sessionId":"s"}
{"type":"user","cwd":"/Users/john/Git/rigsmith","isSidechain":false,"sessionId":"s"}
`
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, p, content)
	cwd, ok, _ := CwdFromTranscript(p)
	if !ok || cwd != "/Users/john/Git/rigsmith" {
		t.Fatalf("got cwd=%q ok=%v", cwd, ok)
	}
}

func TestCwdFromTranscript_HandlesLongLeadingLine(t *testing.T) {
	// A very long first line (big assistant message) must not break the scan.
	long := `{"type":"assistant","text":"` + strings.Repeat("a", 200000) + `","sessionId":"s"}`
	content := long + "\n" + `{"type":"user","cwd":"/home/john/x","isSidechain":false}` + "\n"
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, p, content)
	cwd, ok, err := CwdFromTranscript(p)
	if err != nil || !ok || cwd != "/home/john/x" {
		t.Fatalf("got cwd=%q ok=%v err=%v", cwd, ok, err)
	}
}

func TestCwdFromTranscript_NoCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, p, `{"type":"queue-operation"}`+"\n")
	if _, ok, _ := CwdFromTranscript(p); ok {
		t.Fatal("expected no cwd")
	}
}

func TestCwdFromProjectDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "not-a-transcript.txt"), "ignore me")
	writeFile(t, filepath.Join(dir, "a.jsonl"), sampleTranscript)
	cwd, ok, err := CwdFromProjectDir(dir)
	if err != nil || !ok || cwd != "/Users/john/Git/rigsmith" {
		t.Fatalf("got cwd=%q ok=%v err=%v", cwd, ok, err)
	}
}

func TestRewriteFromTemplate(t *testing.T) {
	tgt := pathmap.NewResolver(pathmap.MapFolders{"HOME": `C:\Users\John`}, pathmap.OSWindows, nil)
	slug, cwd, st := RewriteFromTemplate("$HOME/Git/rigsmith", tgt)
	if st != pathmap.StatusResolved || cwd != `C:\Users\John\Git\rigsmith` || slug != "C--Users-John-Git-rigsmith" {
		t.Fatalf("got slug=%q cwd=%q st=%v", slug, cwd, st)
	}
}
