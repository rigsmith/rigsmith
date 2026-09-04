package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/guard"
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
	if len(added) != 4 || added[0] != "PreToolUse" || added[1] != "SessionEnd" || added[2] != "SessionStart" || added[3] != "Stop" {
		t.Fatalf("added = %v", added)
	}
	// re-install is a no-op
	added2, _ := Install(p, DefaultPlans())
	if len(added2) != 0 {
		t.Fatalf("re-install should add nothing, added %v", added2)
	}
	present, _ := Status(p)
	if len(present) != 4 {
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
	if len(removed) != 4 {
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

// The failure this pair exists to prevent: a machine that installed the guard
// last release keeps that release's matcher forever, so a tool added to the plan
// since is unguarded there while the release notes say it is covered.
func TestInstall_UpdatesAnAlreadyInstalledHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	old := []Plan{{Event: "PreToolUse", Matcher: "Bash", Command: "clauderig guard"}}
	if _, err := Install(path, old); err != nil {
		t.Fatal(err)
	}

	current := []Plan{{Event: "PreToolUse", Matcher: "Bash|Monitor", Command: "clauderig guard"}}
	drift, err := Drift(path, current)
	if err != nil || len(drift) != 1 || drift[0] != "PreToolUse" {
		t.Fatalf("Drift = %v, %v; want [PreToolUse] — a stale matcher is drift", drift, err)
	}

	added, updated, err := InstallOrUpdate(path, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(updated) != 1 {
		t.Fatalf("added=%v updated=%v; want nothing added and PreToolUse updated", added, updated)
	}
	if got := matcherOf(t, path, "PreToolUse"); got != "Bash|Monitor" {
		t.Fatalf("matcher = %q, want the current plan's", got)
	}
	if drift, _ := Drift(path, current); len(drift) != 0 {
		t.Fatalf("still drifted after update: %v", drift)
	}
}

// Updating must never reach into a hook clauderig does not own.
func TestInstall_LeavesForeignHooksAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":[
		{"matcher":"Bash","hooks":[{"type":"command","command":"someone-elses-tool"}]},
		{"matcher":"Bash","hooks":[{"type":"command","command":"clauderig guard"}]}
	]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallOrUpdate(path, []Plan{{Event: "PreToolUse", Matcher: "Bash|Monitor", Command: "clauderig guard"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	groups := s["hooks"].(map[string]any)["PreToolUse"].([]any)
	foreign := groups[0].(map[string]any)
	if m, _ := foreign["matcher"].(string); m != "Bash" {
		t.Fatalf("foreign hook's matcher = %q, want it untouched at \"Bash\"", m)
	}
	ours := groups[1].(map[string]any)
	if m, _ := ours["matcher"].(string); m != "Bash|Monitor" {
		t.Fatalf("our matcher = %q, want updated", m)
	}
}

func matcherOf(t *testing.T, path, event string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	for _, g := range s["hooks"].(map[string]any)[event].([]any) {
		gm := g.(map[string]any)
		if hasMarker(gm) {
			m, _ := gm["matcher"].(string)
			return m
		}
	}
	return ""
}

// A plan that DROPS the matcher has to remove the field. Left in place, the hook
// stays scoped to the old tool list and may never fire — while Install reports
// success, which is the same silent-failure shape as a stale matcher.
func TestInstall_RemovesAMatcherThePlanNoLongerHas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := Install(path, []Plan{{Event: "PreToolUse", Matcher: "Bash", Command: "clauderig guard"}}); err != nil {
		t.Fatal(err)
	}
	unscoped := []Plan{{Event: "PreToolUse", Command: "clauderig guard"}}

	drift, err := Drift(path, unscoped)
	if err != nil || len(drift) != 1 {
		t.Fatalf("Drift = %v, %v; want [PreToolUse] — still scoped when the plan says otherwise", drift, err)
	}
	if _, updated, err := InstallOrUpdate(path, unscoped); err != nil || len(updated) != 1 {
		t.Fatalf("updated = %v, err = %v; want PreToolUse corrected", updated, err)
	}
	if got := matcherOf(t, path, "PreToolUse"); got != "" {
		t.Fatalf("matcher = %q, want the field removed", got)
	}
	if drift, _ := Drift(path, unscoped); len(drift) != 0 {
		t.Fatalf("still drifted: %v", drift)
	}
}

// The matcher is generated from the guard's registry rather than restated, so
// this asserts the wiring rather than a hand-copied list.
func TestGuardPlans_MatcherComesFromTheGuardRegistry(t *testing.T) {
	var matcher string
	for _, p := range GuardPlans() {
		if p.Event == "PreToolUse" {
			matcher = p.Matcher
		}
	}
	if matcher == "" {
		t.Fatal("no PreToolUse matcher in GuardPlans")
	}
	listed := map[string]bool{}
	for _, name := range strings.Split(matcher, "|") {
		listed[name] = true
	}
	for _, tool := range guard.Tools() {
		if !listed[tool] {
			t.Errorf("%s is governed by the guard but missing from the matcher — the hook would never fire for it", tool)
		}
	}
	if !listed["Monitor"] {
		t.Error("Monitor must be in the matcher: it runs shell commands like Bash")
	}
}
