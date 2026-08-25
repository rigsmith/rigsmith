//go:build darwin

package account

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestQuoteForSecurity(t *testing.T) {
	cases := map[string]string{
		"Claude Code-credentials": `"Claude Code-credentials"`, // space → must stay one arg
		"john":                    `"john"`,
		`a"b`:                     `"a\"b"`, // embedded quote escaped
		`a\b`:                     `"a\\b"`, // embedded backslash escaped
	}
	for in, want := range cases {
		if got := quoteForSecurity(in); got != want {
			t.Errorf("quoteForSecurity(%q) = %q, want %q", in, got, want)
		}
	}
}

// The credential is carried as hex (via -X), so any bytes round-trip exactly —
// no escaping of the JSON blob's quotes/braces is needed.
func TestHexCarriesBlobExactly(t *testing.T) {
	blob := sampleBlob("tok", "max")
	decoded, err := hex.DecodeString(hex.EncodeToString(blob))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(blob) {
		t.Fatal("hex round-trip altered the credential blob")
	}
}

// Empirical vector: on 2026-08-18 Claude Code created exactly this service for
// exactly this CLAUDE_CONFIG_DIR. If this test breaks, Claude Code changed its
// per-profile Keychain naming and sessionstore.go needs re-verifying.
func TestSessionKeychainService(t *testing.T) {
	got := sessionKeychainService("/Users/john/.clauderig/accounts/john-brightshore-io/config")
	if got != "Claude Code-credentials-c890e741" {
		t.Fatalf("sessionKeychainService = %q, want Claude Code-credentials-c890e741", got)
	}
}

// security(1) prints the whole blob as hex whenever it holds a byte it won't
// print inline — a newline is enough. Claude Code writes the credential as
// pretty-printed JSON, so the live item comes back hex and the JSON parser
// failed on '{' (0x7b) read as 7 then 'b': "invalid character 'b' after
// top-level value". Observed live on 2026-08-25, Claude Code 2.1.237.
func TestDecodeSecurityOutput(t *testing.T) {
	pretty := "{\n  \"claudeAiOauth\": {\n    \"accessToken\": \"tok\"\n  },\n  \"organizationUuid\": \"f1eab509\"\n}"
	compact := `{"claudeAiOauth":{"accessToken":"tok"},"organizationUuid":"f1eab509"}`

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"hex fallback decodes", hex.EncodeToString([]byte(pretty)), pretty},
		{"compact json passes through", compact, compact},
		{"pretty json passes through", pretty, pretty},
		// Not the hex form: leave it exactly as it came, never half-decode it.
		{"odd length is not hex", "abc", "abc"},
		{"non-hex letters pass through", "zzzz", "zzzz"},
		{"empty passes through", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(decodeSecurityOutput([]byte(c.in))); got != c.want {
				t.Errorf("decodeSecurityOutput() = %q, want %q", got, c.want)
			}
		})
	}
}

// The end-to-end shape of the bug: what security(1) hands back must parse.
func TestDecodeSecurityOutput_HexBlobParsesAsCredential(t *testing.T) {
	pretty := "{\n  \"claudeAiOauth\": {\n    \"accessToken\": \"tok\",\n    \"refreshToken\": \"ref\"\n  },\n  \"organizationUuid\": \"f1eab509\"\n}"
	raw := decodeSecurityOutput([]byte(hex.EncodeToString([]byte(pretty))))
	var v struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
		OrganizationUUID string `json:"organizationUuid"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decoded credential should parse: %v", err)
	}
	if v.ClaudeAiOauth.AccessToken != "tok" || v.OrganizationUUID != "f1eab509" {
		t.Errorf("round trip lost fields: %+v", v)
	}
}
