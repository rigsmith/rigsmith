package gowork

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTools(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.work", "go 1.26\n\nuse (\n\t./cli\n\t./core\n\t./scripts/dev-install\n)\n")
	write("cli/main.go", "// Command rig is the launcher.\npackage main\n")
	write("core/doc.go", "package core\n") // library: no main.go → omitted
	write("scripts/dev-install/main.go", "// Command dev-install installs things.\npackage main\n")

	got, err := Tools(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cli": "rig", "scripts/dev-install": "dev-install"}
	if len(got) != len(want) {
		t.Fatalf("got %d tools (%v), want %d", len(got), got, len(want))
	}
	for _, tool := range got {
		if want[tool.Module] != tool.Name {
			t.Errorf("tool %q = %q, want %q", tool.Module, tool.Name, want[tool.Module])
		}
	}
}

func TestFindRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.work"), []byte("go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	// macOS /tmp symlinks to /private/tmp; compare resolved paths.
	gotR, _ := filepath.EvalSymlinks(got)
	repoR, _ := filepath.EvalSymlinks(repo)
	if gotR != repoR {
		t.Errorf("FindRoot = %q, want %q", gotR, repoR)
	}

	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Error("expected error when no go.work above dir")
	}
}
