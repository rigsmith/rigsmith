package auth

import "testing"

func TestRedactor(t *testing.T) {
	r := NewRedactor()
	r.Add("")         // ignored
	r.Add("   ")      // ignored
	r.Add("abc")      // shorter
	r.Add("abcdefgh") // longer — must be redacted first so "abc" doesn't shadow it
	r.Add("abcdefgh") // dupe — ignored

	got := r.Redact("see abcdefgh and abc")
	want := "see *** and ***"
	if got != want {
		t.Errorf("Redact = %q, want %q", got, want)
	}
}

func TestRedactor_NoSecrets(t *testing.T) {
	r := NewRedactor()
	if got := r.Redact("nothing to hide"); got != "nothing to hide" {
		t.Errorf("Redact = %q, want unchanged", got)
	}
}
