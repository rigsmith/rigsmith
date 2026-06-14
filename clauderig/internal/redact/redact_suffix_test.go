package redact

import (
	"encoding/json"
	"testing"
)

// Suffix matching (isSecretKey) redacts compound/camelCase secret key names that
// the exact SecretKeys map misses, while leaving telemetry/non-secret keys alone.
// Key-name matching is independent of the value's length or entropy, so a short
// scalar value still gets redacted.
func TestRedact_SuffixSecretKeys(t *testing.T) {
	// Compound/camelCase names that must redact via suffix matching.
	redacted := []string{
		"secretKey", "apiToken", "githubToken",
		"clientSecret", "privateKey", "accessToken",
	}
	// Exact policy keys that must still redact.
	exact := []string{"token", "apiKey", "api_key"}
	// Telemetry / non-secret names that must pass through unchanged.
	clean := []string{"maxTokens", "tokenCount", "publicKey", "name", "model"}

	obj := map[string]any{}
	for _, k := range redacted {
		obj[k] = "abc123"
	}
	for _, k := range exact {
		obj[k] = "abc123"
	}
	for _, k := range clean {
		obj[k] = "abc123"
	}
	in, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := RedactBytes(in, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}

	for _, k := range redacted {
		if got[k] != Placeholder {
			t.Errorf("suffix secret key %q not redacted: %v", k, got[k])
		}
	}
	for _, k := range exact {
		if got[k] != Placeholder {
			t.Errorf("exact secret key %q not redacted: %v", k, got[k])
		}
	}
	for _, k := range clean {
		if got[k] != "abc123" {
			t.Errorf("non-secret key %q clobbered: %v", k, got[k])
		}
	}
}
