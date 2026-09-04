package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Scope{"user": User, "project": Project, "local": Local, "global": User} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := Parse("nope"); err == nil {
		t.Error("Parse(nope) should error")
	}
}

func TestPath(t *testing.T) {
	home, root := "/home/u", "/repo"
	cases := []struct {
		scope Scope
		want  string
	}{
		{User, filepath.Join(home, ".claude", "settings.json")},
		{Project, filepath.Join(root, ".claude", "settings.json")},
		{Local, filepath.Join(root, ".claude", "settings.local.json")},
	}
	for _, c := range cases {
		got, err := c.scope.Path(home, root)
		if err != nil || got != c.want {
			t.Errorf("%s.Path = %q, %v; want %q", c.scope, got, err, c.want)
		}
	}
	// Project/Local require a repo root.
	if _, err := Project.Path(home, ""); err == nil {
		t.Error("Project.Path with no root should error")
	}
	if _, err := Local.Path(home, ""); err == nil {
		t.Error("Local.Path with no root should error")
	}
	if _, err := User.Path("", root); err == nil {
		t.Error("User.Path with no home should error")
	}
}

func TestIgnoredAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if got, err := IgnoredAt(Project, path); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	// The shape Claude Code actually writes: the mode lives under permissions.
	if err := os.WriteFile(path, []byte(`{"permissions": {"defaultMode": "bypassPermissions", "allow": ["Bash"]}, "hooks": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Scope{Project, Local} {
		got, err := IgnoredAt(s, path)
		if err != nil || len(got) != 1 || got[0].Key != "permissions.defaultMode" || got[0].Value != "bypassPermissions" {
			t.Errorf("%s: got %v, %v; want permissions.defaultMode flagged", s, got, err)
		}
	}
	if got, err := IgnoredAt(User, path); err != nil || len(got) != 0 {
		t.Errorf("user scope honours it: got %v, %v", got, err)
	}
	// A top-level defaultMode is a mistake Claude Code never reads, but a
	// person who wrote it meant the same thing, so it is reported rather than
	// silently passed over.
	if err := os.WriteFile(path, []byte(`{"defaultMode": "auto"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(Project, path); err != nil || len(got) != 1 || got[0].Key != "defaultMode" {
		t.Errorf("top-level defaultMode: got %v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions": {"defaultMode": "acceptEdits"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(Project, path); err != nil || len(got) != 0 {
		t.Errorf("acceptEdits is honoured everywhere: got %v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := IgnoredAt(Project, path); err == nil {
		t.Error("malformed file reported as clean")
	}
}
