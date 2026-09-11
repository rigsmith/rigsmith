package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func writeFixture(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	// Deliberately invalid config bytes: inventory must not parse file contents.
	if err := os.WriteFile(p, []byte("PRIVATE_FIXTURE_CONTENT_MUST_NOT_APPEAR\xff"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryUsesSharedSelectionWithoutContents(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"config.toml", "work.config.toml", "AGENTS.md", "hooks.json", "rules/default.rules", "skills/demo/SKILL.md", "auth.json", "state_5.sqlite", "sessions/2026/09/11/rollout.jsonl", "plugins/cache/pkg/SKILL.md", "skills/demo/.env", "skills/.system/tool/SKILL.md", "skills/demo/node_modules/pkg/file"} {
		writeFixture(t, root, rel)
	}
	got, err := Inspect(t.Context(), Root{CodexHome, root})
	if err != nil || !got.Present {
		t.Fatal(got, err)
	}
	var paths []string
	for _, c := range got.Candidates {
		paths = append(paths, c.Path)
	}
	want := []string{"AGENTS.md", "config.toml", "hooks.json", "rules/default.rules", "skills/demo/SKILL.md", "work.config.toml"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatal(paths)
	}
	for _, rel := range want {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || string(data) != "PRIVATE_FIXTURE_CONTENT_MUST_NOT_APPEAR\xff" {
			t.Fatal("inventory changed source", rel, err)
		}
	}
	shared := t.TempDir()
	for _, rel := range []string{"demo/SKILL.md", "demo/assets/icon.svg", "README.md", "demo/credentials.json"} {
		writeFixture(t, shared, rel)
	}
	got, err = Inspect(t.Context(), Root{UserSkills, shared})
	if err != nil || len(got.Candidates) != 2 {
		t.Fatal(got, err)
	}
}

func TestInventoryLinksAreNotCandidates(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	writeFixture(t, target, "SKILL.md")
	if err := os.Mkdir(filepath.Join(root, "skills"), 0700); err != nil {
		t.Fatal(err)
	}
	for p, to := range map[string]string{"config.toml": filepath.Join(target, "SKILL.md"), "skills/linked": target} {
		if err := os.Symlink(to, filepath.Join(root, filepath.FromSlash(p))); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("symlinks unavailable", err)
			}
			t.Fatal(err)
		}
	}
	got, err := Inspect(t.Context(), Root{CodexHome, root})
	if err != nil || len(got.Candidates) != 0 {
		t.Fatal(got, err)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked-root")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), Root{CodexHome, linkedRoot}); err == nil {
		t.Fatal("accepted linked root")
	}
}

func TestRootsAndMissingSources(t *testing.T) {
	home := t.TempDir()
	roots, err := Roots(home, "", "")
	if err != nil || roots[0].Path != filepath.Join(home, ".codex") || roots[1].Path != filepath.Join(home, ".agents", "skills") {
		t.Fatal(roots, err)
	}
	for _, root := range roots {
		got, err := Inspect(t.Context(), root)
		if err != nil || got.Present || got.Candidates == nil {
			t.Fatal(got, err)
		}
		if _, err := os.Lstat(root.Path); !os.IsNotExist(err) {
			t.Fatal("created missing source", err)
		}
	}
	custom := t.TempDir()
	roots, err = Roots(home, custom, "")
	if err != nil || roots[0].Path != custom || roots[1].Path != filepath.Join(home, ".agents", "skills") {
		t.Fatal(roots, err)
	}
	for _, args := range [][3]string{{"relative", "", ""}, {home, "relative", ""}, {home, "", "relative"}} {
		if _, err := Roots(args[0], args[1], args[2]); err == nil {
			t.Fatal("accepted relative root", args)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, Root{CodexHome, filepath.Join(home, "missing")}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
