package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestInstallWritesTheShapeCodexActuallyReads(t *testing.T) {
	// PascalCase keys under a top-level "hooks" object. camelCase and
	// snake_case are both parsed and SILENTLY IGNORED, so getting this wrong
	// produces no error, no warning, and no hook.
	path := filepath.Join(t.TempDir(), FileName)
	added, updated, err := Install(path, SyncPlans())
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || len(updated) != 0 {
		t.Fatalf("added=%v updated=%v, want both sync hooks added", added, updated)
	}
	doc := readDoc(t, path)
	h, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("no top-level \"hooks\" object: %v", doc)
	}
	for _, want := range []string{"SessionStart", "Stop"} {
		if _, ok := h[want]; !ok {
			t.Errorf("no %q key; Codex would read this file and install nothing", want)
		}
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), `"async"`) {
		// Parsed, then skipped with a warning in 0.144.6.
		t.Error("an async hook was written; Codex refuses to run those")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if _, _, err := Install(path, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	added, updated, err := Install(path, SyncPlans())
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(updated) != 0 {
		t.Errorf("a second install reported added=%v updated=%v", added, updated)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a second install rewrote the file, which would invalidate its trust hash every run")
	}
}

func TestInstallLeavesSomebodyElsesHooksAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	theirs := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"their-tool --notify"}]}],` +
		`"PostToolUse":[{"hooks":[{"type":"command","command":"their-tool --after"}]}]}}`
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(path, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, path)
	h := doc["hooks"].(map[string]any)
	stop := h["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop has %d group(s), want theirs plus ours", len(stop))
	}
	if _, ok := h["PostToolUse"]; !ok {
		t.Error("an unrelated event was dropped")
	}

	removed, err := Uninstall(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) == 0 {
		t.Fatal("uninstall removed nothing")
	}
	doc = readDoc(t, path)
	h = doc["hooks"].(map[string]any)
	stop = h["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("Stop has %d group(s) after uninstall, want theirs alone", len(stop))
	}
	if !strings.Contains(mustJSON(t, stop[0]), "their-tool") {
		t.Error("uninstall removed the wrong group")
	}
}

func TestUninstallLeavesNoEmptyEventBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if _, _, err := Install(path, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(path); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, path)
	h, _ := doc["hooks"].(map[string]any)
	if _, present := h["Stop"]; present {
		t.Error("Stop was left as an empty array; another reader should see it as absent")
	}
}

func TestDriftIsAboutStalenessNotAbsence(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if _, _, err := Install(path, []Plan{{Event: Stop, Command: "codexrig sync"}}); err != nil {
		t.Fatal(err)
	}
	// The plan changed. An installed hook that no longer matches is drift.
	drifted, err := Drift(path, []Plan{{Event: Stop, Command: "codexrig sync --hook"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifted) != 1 || drifted[0] != "Stop" {
		t.Errorf("Drift = %v, want [Stop]", drifted)
	}
	// An event with no codexrig hook at all is NOT drift — that is Install's
	// business, and conflating them makes a partial install and a stale one
	// look like the same problem.
	drifted, err = Drift(path, []Plan{{Event: SessionStart, Command: "codexrig pull"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifted) != 0 {
		t.Errorf("Drift = %v for an absent hook, want none", drifted)
	}
	// And installing over the drift fixes it rather than duplicating it.
	_, updated, err := Install(path, []Plan{{Event: Stop, Command: "codexrig sync --hook"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 {
		t.Fatalf("updated = %v, want the drifted hook rewritten", updated)
	}
	doc := readDoc(t, path)
	if got := len(doc["hooks"].(map[string]any)["Stop"].([]any)); got != 1 {
		t.Errorf("Stop has %d groups after the fix, want 1", got)
	}
}

func TestAMatcherIsWrittenAndOmittedCorrectly(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if _, _, err := Install(path, append(SyncPlans(), GuardPlans("Bash|apply_patch")...)); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, path)
	h := doc["hooks"].(map[string]any)
	guard := h["PreToolUse"].([]any)[0].(map[string]any)
	if guard["matcher"] != "Bash|apply_patch" {
		t.Errorf("matcher = %v", guard["matcher"])
	}
	stop := h["Stop"].([]any)[0].(map[string]any)
	if _, present := stop["matcher"]; present {
		t.Error("an empty matcher was written; it should be omitted entirely")
	}
}

func TestAnUnparseableFileIsRefusedNotReplaced(t *testing.T) {
	// Somebody's work in progress. Overwriting it is the one unrecoverable
	// thing this package could do.
	path := filepath.Join(t.TempDir(), FileName)
	broken := "{ this is not json"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(path, SyncPlans()); err == nil {
		t.Fatal("expected a refusal")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != broken {
		t.Error("the unparseable file was rewritten anyway")
	}
}

func TestTrustKeyUsesSnakeCaseBecauseCodexDoes(t *testing.T) {
	// The file says PascalCase and the trust key says snake_case. They really
	// do disagree, and hard-coding one for both writes a trust entry naming a
	// hook that does not exist.
	cases := map[Event]string{
		SessionStart:     "session_start",
		Stop:             "stop",
		PreToolUse:       "pre_tool_use",
		UserPromptSubmit: "user_prompt_submit",
	}
	for e, want := range cases {
		if got := TrustKeyEvent(e); got != want {
			t.Errorf("TrustKeyEvent(%s) = %q, want %q", e, got, want)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
