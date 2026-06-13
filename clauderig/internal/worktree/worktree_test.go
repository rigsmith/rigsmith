package worktree

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"feat/x":             "feat-x",
		"feat/ecosystem":     "feat-ecosystem",
		"a/b/c":              "a-b-c",
		"plain":              "plain",
		"/leading-trailing/": "leading-trailing",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathFor(t *testing.T) {
	got := PathFor("/Users/john/Git/rigsmith", "feat/x")
	want := "/Users/john/Git/rigsmith-worktrees/feat-x"
	if got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
	// Trailing slash on the root must not change the layout.
	if got := PathFor("/Users/john/Git/rigsmith/", "main"); got != "/Users/john/Git/rigsmith-worktrees/main" {
		t.Errorf("PathFor with trailing slash = %q", got)
	}
}
