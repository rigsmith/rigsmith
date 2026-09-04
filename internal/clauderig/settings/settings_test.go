package settings

import (
	"os"
	"path/filepath"
	"strings"
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
	// A top-level defaultMode is a mistake at user scope too: the file is
	// parsed there, and only the nested key is exempt.
	if err := os.WriteFile(path, []byte(`{"defaultMode": "bypassPermissions"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(User, path); err != nil || len(got) != 1 || got[0].Key != "defaultMode" || got[0].Where != "" || got[0].Fix == "" {
		t.Errorf("user scope, top-level key: got %+v, %v; want it reported with a fix and no other scope named", got, err)
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
	// Whatever its value: the key itself is never read.
	if err := os.WriteFile(path, []byte(`{"defaultMode": "acceptEdits"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(Local, path); err != nil || len(got) != 1 || got[0].Key != "defaultMode" || got[0].Where != "" || !strings.Contains(got[0].Fix, "permissions") {
		t.Errorf("top-level defaultMode with an honoured value: got %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions": {"defaultMode": "acceptEdits"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(Project, path); err != nil || len(got) != 0 {
		t.Errorf("acceptEdits is honoured everywhere: got %v, %v", got, err)
	}
	// Present and empty is still present: the key is the mistake.
	if err := os.WriteFile(path, []byte(`{"defaultMode": ""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := IgnoredAt(Project, path); err != nil || len(got) != 1 || got[0].Key != "defaultMode" || got[0].Fix == "" {
		t.Errorf("empty top-level defaultMode: got %+v, %v; want it reported", got, err)
	}
	// JSON keys are case-sensitive and so is Claude Code, though a Go struct
	// would not be: a DefaultMode is not the setting at any scope, and the
	// advice is the spelling, not the scope.
	if err := os.WriteFile(path, []byte(`{"permissions": {"DefaultMode": "bypassPermissions"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Scope{User, Project} {
		got, err := IgnoredAt(s, path)
		if err != nil || len(got) != 1 || got[0].Key != "permissions.DefaultMode" || got[0].Where != "" || !strings.Contains(got[0].Fix, "case-sensitive") {
			t.Errorf("%s, wrong-case key: got %+v, %v; want it reported as a spelling mistake, not a scope one", s, got, err)
		}
	}
	// A value of the wrong type does not parse as settings either.
	if err := os.WriteFile(path, []byte(`{"permissions": {"defaultMode": 42}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := IgnoredAt(Project, path); err == nil || !IsParseError(err) {
		t.Errorf("wrong-typed value: err = %v, want a parse error", err)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := IgnoredAt(Project, path); err == nil || !IsParseError(err) {
		t.Errorf("malformed file: err = %v, want a parse error", err)
	}
	// A path that cannot be read is an error too, but not a parse error.
	if _, err := IgnoredAt(Project, filepath.Dir(path)); err == nil || IsParseError(err) {
		t.Errorf("unreadable path: err = %v, want a read error", err)
	}
}
