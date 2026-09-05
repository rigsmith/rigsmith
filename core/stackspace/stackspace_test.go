package stackspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFind(t *testing.T) {
	t.Run("not a stackspace", func(t *testing.T) {
		s, err := Find(t.TempDir())
		if err != nil || s != nil {
			t.Fatalf("Find = %+v, %v; want nil, nil", s, err)
		}
	})

	t.Run("a dedicated manifest names the members", func(t *testing.T) {
		root := t.TempDir()
		body := "{\n  // jsonc\n  \"repos\": { \"pty-core\": { \"upstream\": \"h/a/pty\" }, \"term-core/\": {} },\n}\n"
		if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Find(root)
		if err != nil || s == nil {
			t.Fatalf("Find = %+v, %v", s, err)
		}
		if len(s.Members) != 2 || s.Members[0] != "pty-core" || s.Members[1] != "term-core" {
			t.Fatalf("Members = %v", s.Members)
		}
		for rel, want := range map[string]string{
			"pty-core":                           "pty-core",
			"pty-core/src/Pty.Core/Pty.csproj":   "pty-core",
			filepath.Join("term-core", "go.mod"): "term-core",
			"pty-core-docs/readme.md":            "",
			"Directory.Build.props":              "",
			".":                                  "",
		} {
			if got := s.MemberOf(rel); got != want {
				t.Errorf("MemberOf(%q) = %q, want %q", rel, got, want)
			}
		}
	})

	t.Run("an inline stack block in .rig.json counts too", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".rig.json"), []byte(`{"stack": {"repos": {"lib": {}}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Find(root)
		if err != nil || s == nil || len(s.Members) != 1 || s.Members[0] != "lib" {
			t.Fatalf("Find = %+v, %v", s, err)
		}
	})

	t.Run("a manifest that cannot be parsed is an error, not silence", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), []byte(`{"repos": `), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Find(root); err == nil {
			t.Fatal("a broken manifest read as not-a-stackspace")
		}
	})

	t.Run("a nil stackspace owns nothing", func(t *testing.T) {
		var s *Stackspace
		if s.Owns("anything") {
			t.Fatal("nil stackspace owned a path")
		}
	})
}
