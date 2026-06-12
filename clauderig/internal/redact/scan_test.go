package redact

import "testing"

func TestLooksSecret_Positives(t *testing.T) {
	cases := map[string]string{
		"sk-ant-api03-abcdefghij":                  "anthropic-key",
		"sk-proj-abcdefghijklmnop":                 "openai-key",
		"ghp_abcdefghijklmnopqrst":                 "github-token",
		"github_pat_abcdefghij1234":                "github-pat",
		"AKIAIOSFODNN7EXAMPLE":                     "aws-key",
		"Bearer abcdefghijklmnopqrstuvwxyz":        "bearer",
		"eyJhbGci.eyJzdWIxMjM0.SflKxwRJSMeKKF2QT4": "jwt",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc": "private-key",
	}
	for s, want := range cases {
		if kind, ok := LooksSecret(s); !ok || kind != want {
			t.Errorf("LooksSecret(%q) = (%q,%v), want %q", s, kind, ok, want)
		}
	}
}

func TestLooksSecret_HighEntropy(t *testing.T) {
	// A 48-char opaque base64-ish blob with no prefix should trip.
	if _, ok := LooksSecret("Zk9q3xR7tLmA1cD8eF0gH2iJ4kL6mN8oP0qR2sT4uV6wX8y"); !ok {
		t.Error("expected high-entropy blob to trip")
	}
}

func TestLooksSecret_Negatives(t *testing.T) {
	// Things that must NOT trip: UUID, file paths, short/plain strings, redaction
	// placeholder, low-entropy repeats.
	negatives := []string{
		"03d1c0c9-823d-464b-a468-a9bea2383338", // account UUID
		"/Users/john/Git/rigsmith/some/long/path/here/file.go",
		"high",
		"acceptEdits",
		Placeholder,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // long but ~0 entropy
		"claude-fable-5[1m]",
	}
	for _, s := range negatives {
		if kind, ok := LooksSecret(s); ok {
			t.Errorf("LooksSecret(%q) falsely tripped as %q", s, kind)
		}
	}
}
