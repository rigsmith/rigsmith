//go:build darwin

package account

import (
	"encoding/hex"
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
