package redact

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStreamCompleteAndBoundaries(t *testing.T) {
	secret := "ghp_" + strings.Repeat("x", 36)
	for _, pad := range []int{0, 32765, 65533, 5 << 20} {
		s := strings.Repeat("ordinary prose. ", pad/16) + strings.Repeat(" ", pad%16) + secret
		f, err := ScanReader("projects/p/chat.jsonl", strings.NewReader(s))
		if err != nil || f == nil || f.Kind != "github-token" {
			t.Fatalf("offset %d: %v %v", pad, f, err)
		}
		if strings.Contains(f.Path, secret) {
			t.Fatal("secret in diagnostic")
		}
	}
	f, err := ScanReader("settings.toml", strings.NewReader("[mcp_servers.x]\nkey = \""+secret+"\"\n"))
	if err != nil || f == nil {
		t.Fatalf("TOML inline token: %v %v", f, err)
	}
}
func TestStreamEscapedAndLargePEM(t *testing.T) {
	for _, s := range []string{`{"message":"\u0067\u0068\u0070\u005f` + strings.Repeat("a", 36) + `"}`, strings.Repeat("x", 1<<20) + "-----BEGIN RSA PRIVATE KEY-----\n"} {
		f, err := ScanReader("chat.jsonl", strings.NewReader(s))
		if f == nil || err != nil {
			t.Fatalf("%v %v", f, err)
		}
	}
}
func TestStreamBenignAndReadFailure(t *testing.T) {
	s := strings.Repeat("ordinary prose sha512-abc123 user-id-0123456789\n", 100000)
	f, err := ScanReader("chat.jsonl", strings.NewReader(s))
	if f != nil || err != nil {
		t.Fatalf("%v %v", f, err)
	}
	_, err = ScanReader("chat.jsonl", io.MultiReader(strings.NewReader("hello"), badReader{}))
	if err == nil {
		t.Fatal("read failure hidden")
	}
}

type badReader struct{}

func (badReader) Read([]byte) (int, error) { return 0, errors.New("broken source") }

func TestStreamTextNULAndLargeAuthConfig(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"chat.jsonl", "\x00" + strings.Repeat(" ", 70000) + "ghp_" + strings.Repeat("z", 36)},
		{".env", strings.Repeat("# ordinary configuration\n", 3000) + "PASSWORD=not-a-placeholder-value\n"},
		{"chat.jsonl", "authorization: bearer " + strings.Repeat("aB3d", 10)},
	} {
		f, err := ScanReader(tc.path, strings.NewReader(tc.body))
		if err != nil || f == nil {
			t.Fatalf("%s: %v %v", tc.path, f, err)
		}
	}
}

func TestStreamLongJWTAndLegacyBareToken(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9." + strings.Repeat("abcdef", 20000) + ".signature"
	f, err := ScanReader("chat.jsonl", strings.NewReader(jwt))
	if err != nil || f == nil || f.Kind != "jwt" {
		t.Fatalf("long JWT: %v %v", f, err)
	}
	bare := strings.Repeat("aB3dEfG4hIjK5mNoP6qRsT7uVwX8yZ9", 1400)
	f, err = ScanReader("opaque-token", strings.NewReader(bare))
	if err != nil || f == nil {
		t.Fatalf("legacy 32–64 KiB token: %v %v", f, err)
	}
}
