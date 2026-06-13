package settings

import (
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
