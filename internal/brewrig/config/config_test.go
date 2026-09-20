package config

import "testing"

// macOS names a Mac "John's MacBook Pro" with a curly apostrophe, which is a
// poor filename and a worse git path.
func TestSanitizeMachineName(t *testing.T) {
	cases := map[string]string{
		"John’s MacBook Pro":  "Johns-MacBook-Pro",
		"John's MacBook Pro":  "Johns-MacBook-Pro",
		"Johns-MacBook-Pro16": "Johns-MacBook-Pro16",
		"  spaced  ":          "spaced",
		"a/b":                 "ab",
		"../escape":           "escape",
		"":                    "machine",
		"???":                 "machine",
	}
	for in, want := range cases {
		if got := SanitizeMachineName(in); got != want {
			t.Errorf("SanitizeMachineName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The name becomes a path inside the repo, so it must not be able to climb out
// of machines/.
func TestSanitizedNameCannotTraverse(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "a/../../b", "/abs"} {
		got := SanitizeMachineName(in)
		for _, bad := range []string{"/", ".."} {
			if contains(got, bad) {
				t.Errorf("SanitizeMachineName(%q) = %q, still contains %q", in, got, bad)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBranchDefaultsToMain(t *testing.T) {
	if got := (&Config{}).BranchOrDefault(); got != "main" {
		t.Errorf("BranchOrDefault = %q, want main", got)
	}
	if got := (&Config{Branch: "trunk"}).BranchOrDefault(); got != "trunk" {
		t.Errorf("BranchOrDefault = %q, want trunk", got)
	}
}
