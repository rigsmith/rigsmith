package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func queueHookJSON(t *testing.T, event, session, path string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hook_event_name": event, "session_id": session, "transcript_path": path, "last_assistant_message": "private text never saved", "future_field": map[string]any{"value": true}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func executeQueueHook(f *queueCommandFixture, ctx context.Context, in io.Reader, args ...string) (string, string, error) {
	cmd := newQueueCmd(f.deps)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out, diagnostic bytes.Buffer
	cmd.SetIn(in)
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostic)
	cmd.SetArgs(append([]string{"--dir", f.dir, "prepare", "--hook"}, args...))
	err := cmd.ExecuteContext(ctx)
	return out.String(), diagnostic.String(), err
}

func TestQueueHookSavedIntentAndReplay(t *testing.T) {
	for _, event := range []string{"Stop", "SessionEnd"} {
		t.Run(event, func(t *testing.T) {
			f := newQueueFixture(t)
			f.must(t, "init")
			transcript := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
			path := filepath.Join(t.TempDir(), "request.json")
			checks := f.privateChecks
			out, diagnostic, err := executeQueueHook(f, t.Context(), strings.NewReader(queueHookJSON(t, event, "s", transcript)), "--output", path)
			if err != nil || out != "" || !strings.Contains(diagnostic, "Request saved") || f.reads != 1 || f.privateChecks != checks {
				t.Fatal(out, diagnostic, err, f.reads, f.privateChecks)
			}
			saved, err := readQueueRequest(path)
			if err != nil || saved.Identity != f.identity || saved.Request.SessionID != "s" {
				t.Fatal(saved, err)
			}
			want := queue.Normal
			if event == "SessionEnd" {
				want = queue.Selected
				if len(saved.Request.Flush.Paths) != 1 || saved.Request.Flush.Paths[0] != transcript {
					t.Fatal(saved.Request.Flush)
				}
			}
			if saved.Request.Flush.Mode != want {
				t.Fatal(saved.Request.Flush)
			}
			data, _ := os.ReadFile(path)
			if bytes.Contains(data, []byte("private text")) || bytes.Contains(data, []byte("future_field")) {
				t.Fatal("persisted unrelated vendor input")
			}
			pending, err := f.open(t).Snapshot(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatal("preparation enqueued", pending, err)
			}
			// Admission and replay never consult a later account or hook payload.
			f.deps.identity = func() (service.Identity, error) { t.Fatal("replay read identity"); return service.Identity{}, nil }
			first := f.must(t, "enqueue", path)
			if again := f.must(t, "enqueue", path); again != first {
				t.Fatal(first, again)
			}
			pending, err = f.open(t).Snapshot(t.Context())
			if err != nil || len(pending) != 1 || len(pending[0].Events) != 1 || pending[0].Attempts != 0 {
				t.Fatal(pending, err)
			}
		})
	}
}

func TestQueueHookRefusesInputBeforeIdentityOrWrite(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	f.deps.identity = func() (service.Identity, error) {
		t.Fatal("invalid input reached identity")
		return service.Identity{}, nil
	}
	valid := queueHookJSON(t, "SessionEnd", "s", filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl"))
	for name, data := range map[string]string{
		"empty": "", "blank": " \n", "null": "null", "array": "[]", "truncated": "{", "trailing": valid + "{}",
		"missing":      `{ "hook_event_name": "SessionEnd" }`,
		"duplicate":    strings.Replace(valid, `"session_id":"s"`, `"session_id":"s","session_id":"other"`, 1),
		"case-alias":   strings.Replace(valid, `"session_id"`, `"Session_ID"`, 1),
		"wrong-type":   strings.Replace(valid, `"session_id":"s"`, `"session_id":5`, 1),
		"unsupported":  strings.Replace(valid, `"SessionEnd"`, `"SessionStart"`, 1),
		"subagent":     strings.Replace(valid, `{`, `{"agent_id":"agent-a",`, 1),
		"invalid-utf8": valid[:len(valid)-1] + string([]byte{255}) + "}",
		"oversized":    strings.Repeat(" ", queueRequestLimit+1),
		"outside":      queueHookJSON(t, "Stop", "s", filepath.Join(t.TempDir(), "s.jsonl")),
		"mismatch":     queueHookJSON(t, "Stop", "s", filepath.Join(f.req.Machine.Home, ".claude", "projects", "p", "other.jsonl")),
		"relative":     queueHookJSON(t, "Stop", "s", "projects/p/s.jsonl"),
		"nested":       queueHookJSON(t, "Stop", "s", filepath.Join(f.req.Machine.Home, ".claude", "projects", "p", "subagents", "s.jsonl")),
		"control":      strings.Replace(valid, `"session_id":"s"`, `"session_id":"s\n"`, 1),
		"surrogate":    strings.Replace(valid, `"session_id":"s"`, `"session_id":"\ud800"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			out, _, err := executeQueueHook(f, t.Context(), strings.NewReader(data), "--output", path)
			if err == nil || out != "" || strings.Contains(err.Error(), "private text") {
				t.Fatal(out, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid input created request", err)
			}
		})
	}
}

func TestQueueHookReadDeadlineAndCancellation(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel", "open-after-object"} {
		t.Run(mode, func(t *testing.T) {
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() {
				if mode == "open-after-object" {
					_, _ = io.WriteString(w, queueHookJSON(t, "Stop", "s", "/fixture/s.jsonl"))
				}
				if mode == "cancel" {
					cancel()
				}
			}()
			wait := 20 * time.Millisecond
			if mode == "cancel" {
				wait = 2 * time.Second
			}
			_, err := readQueueHook(ctx, r, wait)
			if err == nil || (mode == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatal(err)
			}
		})
	}
}

func TestQueueHookConflictingFlagsAndUnknownIdentity(t *testing.T) {
	f := newQueueFixture(t)
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request.json")
	for _, flag := range []string{"--session=s", "--flush"} {
		if _, _, err := executeQueueHook(f, t.Context(), strings.NewReader(""), "--output", path, flag); err == nil {
			t.Fatal("accepted conflicting flag", flag)
		}
	}
	f.identity = service.Identity{}
	payload := queueHookJSON(t, "Stop", "s", filepath.Join(f.req.Machine.Home, ".claude", "projects", "p", "s.jsonl"))
	if _, _, err := executeQueueHook(f, t.Context(), strings.NewReader(payload), "--output", path); err == nil {
		t.Fatal("accepted implicit unknown identity")
	}
	if _, _, err := executeQueueHook(f, t.Context(), strings.NewReader(payload), "--output", path, "--unknown-identity"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, _, err := executeQueueHook(f, t.Context(), strings.NewReader(payload), "--output", path, "--unknown-identity"); !os.IsExist(err) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("overwrote saved request")
	}
}

func TestQueueHookConfiguredSource(t *testing.T) {
	f := newQueueFixture(t)
	f.req.Config.Roots[0].Location.Portable = "$HOME/alternate-cli"
	f.must(t, "init")
	for _, dirname := range []string{".claude", "alternate-cli"} {
		path := filepath.Join(t.TempDir(), "request.json")
		payload := queueHookJSON(t, "Stop", "s", filepath.Join(f.req.Machine.Home, dirname, "projects", "p", "s.jsonl"))
		_, _, err := executeQueueHook(f, t.Context(), strings.NewReader(payload), "--output", path)
		if dirname == ".claude" && !errors.Is(err, queue.ErrBinding) {
			t.Fatal("accepted wrong configured source", err)
		}
		if dirname == "alternate-cli" && err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueueHookSupervisedDrain(t *testing.T) {
	f, runGit := newQueueSupervisedFixture(t)
	dir := filepath.Join(f.req.Machine.Home, ".claude", "projects", "-workspace-acme")
	if err := os.MkdirAll(filepath.Join(dir, "s", "subagents"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"s.jsonl", "s/subagents/agent-a.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte("{\"type\":\"user\",\"sessionId\":\"s\",\"uuid\":\"message-a\",\"cwd\":\"/workspace/acme\",\"message\":{\"role\":\"user\",\"content\":\"queued hook fixture\"}}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.must(t, "init")
	path := filepath.Join(t.TempDir(), "request.json")
	payload := queueHookJSON(t, "SessionEnd", "s", filepath.Join(dir, "s.jsonl"))
	if _, _, err := executeQueueHook(f, t.Context(), strings.NewReader(payload), "--output", path); err != nil {
		t.Fatal(err)
	}
	f.must(t, "enqueue", path)
	f.deps.identity = func() (service.Identity, error) { t.Fatal("worker reread account"); return service.Identity{}, nil }
	f.must(t, "drain")
	pending, err := f.open(t).Snapshot(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	for _, rel := range []string{"s.jsonl", "s/subagents/agent-a.jsonl"} {
		data := runGit("--git-dir", f.req.Config.Remote, "show", "main:cli/projects/-workspace-acme/"+rel)
		if !strings.Contains(data, "queued hook fixture") {
			t.Fatal("missing captured hook session group", rel)
		}
	}
}
