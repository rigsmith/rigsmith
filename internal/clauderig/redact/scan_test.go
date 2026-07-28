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
	// Stripping embedded UUIDs must not open a hole: what's left here is still a
	// 47-char opaque blob.
	if _, ok := LooksSecret("Zk9q3xR7tLmA1cD8eF0gH2iJ4kL6mN8oP0qR2sT4uV6wX8y_b59a1b5b-f0d2-4220-9d4f-236294e64887"); !ok {
		t.Error("expected blob-plus-UUID to trip")
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
		// Desktop names MCP tools mcp__<server-uuid>__<tool>; the UUID is embedded,
		// not a leading prefix, so these must survive UUID stripping.
		"mcp__b59a1b5b-f0d2-4220-9d4f-236294e64887__search_files",
		"mcp__b59a1b5b-f0d2-4220-9d4f-236294e64887__download_file_content",
		// npm lockfile integrity digests: public content hashes, max entropy. Only
		// the ones whose base64 happens to contain no "/" ever reached the entropy
		// check, so this tripped on a random ~20% of any synced lockfile.
		"sha512-ZQBvi1DcpJ4GDqanjucZ2Hj3wEO5pZDS89BWbkcrvdxksJorwUDDZamX9ldFkp9aw2lmBDLgkObEA4DWNJ9FYQ==",
		"sha512-8p0AUk4XODgIewSi0l8Epjs+EVnWiK7NoDIEGU0HhE7+ZyY8D1IMY7odu5lRrFXGg71L15KG8QrPmum45RTtdA==",
		"sha1-Gc0ZS/0+Qo6EqrMmZmKS0X7CpGw=",
	}
	for _, s := range negatives {
		if kind, ok := LooksSecret(s); ok {
			t.Errorf("LooksSecret(%q) falsely tripped as %q", s, kind)
		}
	}
}
