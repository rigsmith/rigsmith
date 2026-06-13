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

func TestQuoteCmd(t *testing.T) {
	if got := QuoteCmd([]string{"code", "-n"}, "/wt/x"); got != "code -n /wt/x" {
		t.Errorf("QuoteCmd = %q", got)
	}
	if got := QuoteCmd([]string{"idea"}, "/wt/x"); got != "idea /wt/x" {
		t.Errorf("QuoteCmd single-arg = %q", got)
	}
}

func TestOpenerAvailable_Empty(t *testing.T) {
	if OpenerAvailable(nil) {
		t.Error("empty open command must never be available")
	}
}
