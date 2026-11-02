package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHardeningScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, file := range []string{"settings.json", "settings.local.json"} {
		for _, linkDir := range []bool{false, true} {
			root := t.TempDir()
			outside := filepath.Join(home, ".claude")
			os.MkdirAll(outside, 0755)
			target := filepath.Join(outside, file)
			os.WriteFile(target, []byte("{}"), 0644)
			if linkDir {
				os.Symlink(outside, filepath.Join(root, ".claude"))
			} else {
				os.MkdirAll(filepath.Join(root, ".claude"), 0755)
				os.Symlink(target, filepath.Join(root, ".claude", file))
			}
			if err := ValidateProject(root, filepath.Join(root, ".claude", file)); err == nil {
				t.Fatal("accepted escaping symlink")
			}
		}
	}
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "repo")
	os.Symlink(root, alias)
	if err := ValidateProject(alias, filepath.Join(alias, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
}
func TestHardeningOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := settingsPath(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo clauderig"},{"type":"command","command":"clauderig sync"},{"type":"command","command":"keep"}]}]}}`)
	if _, err := Install(path, SyncPlans()); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "echo clauderig") || !strings.Contains(string(raw), "keep") || strings.Contains(string(raw), "clauderig sync") {
		t.Fatalf("foreign sibling lost or stale retained: %s", raw)
	}
}
func TestHardeningShapes(t *testing.T) {
	for _, raw := range []string{`null`, `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`, `{"hooks":{"Stop":[null]}}`, `{"hooks":{"Stop":[{"hooks":null}]}}`} {
		p := settingsPath(t, raw)
		if _, err := Install(p, SyncPlans()); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		after, _ := os.ReadFile(p)
		if string(after) != raw {
			t.Fatal("modified invalid file")
		}
	}
	p := settingsPath(t, `{"large":9007199254740993}`)
	Install(p, SyncPlans())
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatal(string(b))
	}
}
func TestHardeningDisplay(t *testing.T) {
	got := Display("a\x1b\n\u202eb")
	if strings.ContainsAny(got, "\x1b\n\u202e") {
		t.Fatal(got)
	}
}
