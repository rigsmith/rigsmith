package devroute

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/john/Git/rigsmith":             "_Users_john_Git_rigsmith",
		"/Users/john/Git/rigsmith-worktrees/x": "_Users_john_Git_rigsmith_worktrees_x",
		"C:\\Users\\john\\Git\\rigsmith":       "C__Users_john_Git_rigsmith",
		"plain":                                "plain",
		"a.b/c-d":                              "a_b_c_d",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRouteFileUnderStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "") // exercise the ~/.local/state fallback
	got, err := RouteFile("/repo/x")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "rigsmith", "dev-routes", "_repo_x")
	if got != want {
		t.Errorf("RouteFile = %q, want %q", got, want)
	}
}

func TestRouteFileHonorsXDGStateHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err := RouteFile("/repo/x")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(xdg, "rigsmith", "dev-routes", "_repo_x")
	if got != want {
		t.Errorf("RouteFile = %q, want %q", got, want)
	}
}

func TestReadWriteUnsetRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const repo = "/Users/john/Git/rigsmith"

	// No pin yet → empty, not an error.
	if got, err := Read(repo); err != nil || got != "" {
		t.Fatalf("Read (unset) = %q, %v; want \"\", nil", got, err)
	}

	const target = "/Users/john/Git/rigsmith-worktrees/feat-x"
	if err := Write(repo, target); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, err := Read(repo); err != nil || got != target {
		t.Fatalf("Read after Write = %q, %v; want %q, nil", got, err, target)
	}

	// Different repo keys to a different file — pins don't bleed across repos.
	if got, _ := Read("/some/other/repo"); got != "" {
		t.Fatalf("Read(other repo) = %q; want \"\"", got)
	}

	if err := Unset(repo); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if got, err := Read(repo); err != nil || got != "" {
		t.Fatalf("Read after Unset = %q, %v; want \"\", nil", got, err)
	}
	// Unsetting again is a no-op, not an error.
	if err := Unset(repo); err != nil {
		t.Fatalf("Unset (already clear): %v", err)
	}
}

func TestReadTrimsWhitespace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const repo = "/r"
	file, err := RouteFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("  /path/with/space\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(repo); got != "/path/with/space" {
		t.Errorf("Read = %q, want trimmed", got)
	}
}
