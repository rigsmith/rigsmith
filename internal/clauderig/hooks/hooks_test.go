package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func settingsPath(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func load_(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstall_FreshAndIdempotent(t *testing.T) {
	p := settingsPath(t, "") // absent file
	added, err := Install(p, DefaultPlans())
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(added)
	if len(added) != 3 || added[0] != "PreToolUse" || added[1] != "SessionStart" || added[2] != "Stop" {
		t.Fatalf("added = %v", added)
	}
	// re-install is a no-op
	added2, _ := Install(p, DefaultPlans())
	if len(added2) != 0 {
		t.Fatalf("re-install should add nothing, added %v", added2)
	}
	present, _ := Status(p)
	if len(present) != 3 {
		t.Fatalf("status = %v", present)
	}
	// the guard hook carries its tool-name matcher
	pre := load_(t, p)["hooks"].(map[string]any)["PreToolUse"].([]any)
	group := pre[0].(map[string]any)
	if m, _ := group["matcher"].(string); m == "" {
		t.Errorf("PreToolUse hook should have a matcher, got %v", group)
	}
}

func TestInstall_PreservesOtherSettingsAndHooks(t *testing.T) {
	existing := `{
		"effortLevel": "high",
		"hooks": {
			"Stop": [ {"hooks":[{"type":"command","command":"my-other-tool"}]} ]
		}
	}`
	p := settingsPath(t, existing)
	if _, err := Install(p, DefaultPlans()); err != nil {
		t.Fatal(err)
	}
	m := load_(t, p)
	if m["effortLevel"] != "high" {
		t.Error("other settings clobbered")
	}
	stop := m["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 { // the user's tool + clauderig
		t.Fatalf("Stop groups = %d, want 2 (other tool preserved)", len(stop))
	}
}

func TestInstall_DoesNotClobberMalformedEvent(t *testing.T) {
	// An event whose value isn't the expected array (malformed / future schema)
	// must be left untouched, not overwritten.
	p := settingsPath(t, `{"hooks":{"Stop":"weird-non-array-value"}}`)
	if _, err := Install(p, DefaultPlans()); err != nil {
		t.Fatal(err)
	}
	m := load_(t, p)
	h := m["hooks"].(map[string]any)
	if h["Stop"] != "weird-non-array-value" {
		t.Errorf("malformed Stop should be preserved, got %v", h["Stop"])
	}
	// SessionStart (well-formed/absent) is still installed
	if _, ok := h["SessionStart"]; !ok {
		t.Error("SessionStart should still be installed")
	}
}

func TestUninstall(t *testing.T) {
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"keep-me"}]}]}}`
	p := settingsPath(t, existing)
	Install(p, DefaultPlans())
	removed, err := Uninstall(p)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if len(removed) != 3 {
		t.Fatalf("removed = %v", removed)
	}
	// the non-clauderig hook survives; SessionStart (clauderig-only) is gone
	m := load_(t, p)
	h := m["hooks"].(map[string]any)
	if _, ok := h["SessionStart"]; ok {
		t.Error("SessionStart should be removed (was clauderig-only)")
	}
	stop := h["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("Stop should keep the one non-clauderig hook, got %d", len(stop))
	}
	if present, _ := Status(p); len(present) != 0 {
		t.Errorf("status after uninstall = %v", present)
	}
}
