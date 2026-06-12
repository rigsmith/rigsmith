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
		"03d1c0c9-823d-464b-a468-a9bea2383338",             // account UUID
		"local_74333a0f-d788-42ac-8da4-0ea39064d471",       // session id: prefix_<uuid>
		"e3055f13cb034ffea75ca73062b8f9ea3a9c7d11deadbeef", // 48-char hex (content hash)
		"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",         // 40-char hex (git SHA)
		"/Users/john/Git/rigsmith/some/long/path/here/file.go",
		"plugins/marketplaces/claude-plugins-official/x.json", // a path value
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
