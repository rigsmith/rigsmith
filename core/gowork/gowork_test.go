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

	write("cmd/rig/main.go", "// Command rig is the launcher.\npackage main\n")
	write("cmd/clauderig/main.go", "// Command clauderig syncs things.\npackage main\n")
	write("cmd/notacmd/doc.go", "package notacmd\n") // no main.go → omitted
	write("core/doc.go", "package core\n")           // outside cmd/ → ignored

	got, err := Tools(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cmd/rig": "rig", "cmd/clauderig": "clauderig"}
	if len(got) != len(want) {
		t.Fatalf("got %d tools (%v), want %d", len(got), got, len(want))
	}
	for _, tool := range got {
		if want[tool.Module] != tool.Name {
			t.Errorf("tool %q = %q, want %q", tool.Module, tool.Name, want[tool.Module])
		}
	}
}

func TestToolsNoCmdDir(t *testing.T) {
	// A repo with no cmd/ directory yields no tools, not an error.
	got, err := Tools(t.TempDir())
	if err != nil {
		t.Fatalf("Tools on cmd-less repo: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d tools, want 0", len(got))
	}
}

func TestFindRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.test\n\ngo 1.26\n"), 0o644); err != nil {
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
		t.Error("expected error when no go.mod above dir")
	}
}
