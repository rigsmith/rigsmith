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
	added, err := Install(p)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(added)
	if len(added) != 2 || added[0] != "SessionStart" || added[1] != "Stop" {
		t.Fatalf("added = %v", added)
	}
	// re-install is a no-op
	added2, _ := Install(p)
	if len(added2) != 0 {
		t.Fatalf("re-install should add nothing, added %v", added2)
	}
	present, _ := Status(p)
	if len(present) != 2 {
		t.Fatalf("status = %v", present)
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
	if _, err := Install(p); err != nil {
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

func TestUninstall(t *testing.T) {
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"keep-me"}]}]}}`
	p := settingsPath(t, existing)
	Install(p)
	removed, err := Uninstall(p)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if len(removed) != 2 {
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
