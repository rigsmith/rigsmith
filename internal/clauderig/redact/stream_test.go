package redact

import (
	"encoding/base64"
	"errors"
	"fmt"
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

func TestStreamLongEscapedJWT(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + strings.Repeat("abcdef", 12000) + ".signature"
	var escaped strings.Builder
	for _, c := range []byte(token) {
		fmt.Fprintf(&escaped, `\u%04x`, c)
	}
	for _, pad := range []int{0, 32762, 32763, 32764, 32765, 32766, 32767} {
		input := strings.Repeat(" ", pad) + `{"message":"` + escaped.String() + `"}`
		f, err := ScanReader("s.jsonl", strings.NewReader(input))
		if err != nil || f == nil || f.Kind != "jwt" {
			t.Fatalf("escaped JWT offset %d missed: %v %v", pad, f, err)
		}
	}
	// Malformed escapes must break a candidate rather than silently join it.
	invalid := `eyJhbGciOiJIUzI1NiJ9.abcde\u00zz` + strings.Repeat("abcdef", 12000) + ".signature"
	if f, err := ScanReader("s.jsonl", strings.NewReader(invalid)); err != nil || f != nil {
		t.Fatalf("invalid escape: %v %v", f, err)
	}
}

func TestStreamBearerKindsIgnoreSchemeCase(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "bEaReR"} {
		f, err := ScanReader("s.jsonl", strings.NewReader(scheme+" "+strings.Repeat("aB3d", 10)))
		if err != nil || f == nil || f.Kind != "bearer" {
			t.Fatalf("scheme %s: %v %v", scheme, f, err)
		}
	}
}

// A verdict about the FILE outranks a value found inside it, wherever each
// appears in the bytes. Returning on the first hit classified a file by which
// credential came first, and the two lead to different remedies: exclude the
// file, or let the redactor scrub the value.
func TestScanReaderPrefersAWholeFileVerdictOverAnEarlierValue(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----\n"

	for _, tc := range []struct {
		name string
		body string
	}{
		{"value first", "a note\n" + jwt + "\nthen\n" + pem},
		{"file marker first", pem + "\nand later\n" + jwt + "\n"},
	} {
		f, err := ScanReader("cli/projects/x/s.jsonl", strings.NewReader(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if f == nil {
			t.Fatalf("%s: nothing found", tc.name)
		}
		if !f.File || f.Kind != "private-key" {
			t.Errorf("%s: finding = %+v, want the whole-file private-key verdict", tc.name, f)
		}
	}
}

// With no file verdict anywhere, the value still comes back — held, not lost.
func TestScanReaderStillReportsAValueWhenNoFileVerdictFollows(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	f, err := ScanReader("cli/projects/x/s.jsonl", strings.NewReader("chat\n"+jwt+"\nmore chat\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Kind != "jwt" {
		t.Fatalf("finding = %+v, want the jwt", f)
	}
	if f.File {
		t.Error("a token inside a transcript was called a whole file")
	}
}
