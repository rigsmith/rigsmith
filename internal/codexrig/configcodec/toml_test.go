package configcodec

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func document(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := toml.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCaptureTOMLTypesAndIdempotence(t *testing.T) {
	source := []byte(`# A comment containing private material is never copied.
model = "example-model"
count = 9223372036854775807
ratio = 0.75
enabled = true
when = 2026-09-11T01:02:03Z
date = 2026-09-11
time = 01:02:03
literal = '''multiple
lines'''
"dotted.key" = { "quoted.key" = "value" }
array = [1, "two", { three = true }]
[[unknown]]
name = "first"
[[unknown]]
name = "second"
[model_providers.example]
env_key = "EXAMPLE_API_KEY"
env_http_headers = { Authorization = "EXAMPLE_AUTH" }
[mcp_servers.example]
bearer_token_env_var = "EXAMPLE_TOKEN"
env_vars = ["EXAMPLE_ENV", { name = "SECOND_ENV", source = "local" }]
`)
	before := bytes.Clone(source)
	got, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document(t, source), document(t, got)) {
		t.Fatalf("lost TOML semantics: %s", got)
	}
	if bytes.Contains(got, []byte("private material")) {
		t.Fatal("comment copied")
	}
	again, err := Capture(got)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("not stable: %v", err)
	}
	if !bytes.Equal(before, source) {
		t.Fatal("input mutated")
	}
}

func TestCaptureOmitsProtectedValuesRegardlessOfType(t *testing.T) {
	for _, key := range []string{"env", "http_headers", "headers", "query_params", "auth", "oauth", "credentials", "apiToken", "experimental_bearer_token", "API.KEY", "command", "args", "http_headers_helper", "hooks", "notify", "projects", "sqlite_home", "cli_auth_credentials_store", "mcp_oauth_credentials_store"} {
		for _, value := range []string{`"short-private"`, `1234`, `true`, `["short-private", 12]`, `{ nested = "short-private" }`} {
			t.Run(key+"/"+value, func(t *testing.T) {
				source := []byte("model = 'portable'\n\"" + key + "\" = " + value + "\n")
				got, err := Capture(source)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(document(t, got), map[string]any{"model": "portable"}) {
					t.Fatalf("protected value escaped: %s", got)
				}
			})
		}
	}
}

func TestCaptureArraySecretsOmitWholeArray(t *testing.T) {
	source := []byte(`model = "portable"
[[entries]]
name = "first"
password = "private"
[[entries]]
name = "second"
[unknown]
keep = "yes"
items = [{ name = "first" }, { env = { SMALL = 1234 } }]
`)
	got, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"model": "portable", "unknown": map[string]any{"keep": "yes"}}
	if !reflect.DeepEqual(document(t, got), want) {
		t.Fatalf("array partially copied: %s", got)
	}
}

func TestCaptureTripwireDecodedKeysAndValues(t *testing.T) {
	token := "sk-" + strings.Repeat("A1", 16)
	for _, source := range []string{
		"unknown = '" + token + "'", "unknown = 'prefix " + token + " suffix'",
		"[\"" + token + "\"]\nx = 'value'", "\"" + token + "\" = 'value'",
		"items = ['prefix " + token + " suffix']",
		`unknown = "\u0073\u006b-` + strings.Repeat("A1", 16) + `"`,
		"unknown = '-----BEGIN PRIVATE KEY-----'",
	} {
		got, err := Capture([]byte(source))
		if !errors.Is(err, ErrSecret) || got != nil {
			t.Fatalf("tripwire bypassed: %v", err)
		}
		if strings.Contains(err.Error(), token) {
			t.Fatal("error leaked secret")
		}
	}
}

func TestRestorePreservesLocalSecretsAndAdditions(t *testing.T) {
	source := []byte(`model = "new"
[env]
API = "source-private"
[unknown]
shared = 2
password = "source-private"
`)
	backup, err := Capture(source)
	if err != nil {
		t.Fatal(err)
	}
	local := []byte(`model = "old"
local_only = true
[env]
API = "destination-private"
[unknown]
shared = 1
password = "destination-private"
extra = "local"
`)
	beforeBackup, beforeLocal := bytes.Clone(backup), bytes.Clone(local)
	got, err := Restore(backup, local)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"model": "new", "local_only": true, "env": map[string]any{"API": "destination-private"}, "unknown": map[string]any{"shared": int64(2), "password": "destination-private", "extra": "local"}}
	if !reflect.DeepEqual(document(t, got), want) {
		t.Fatalf("bad merge: %s", got)
	}
	if !bytes.Equal(backup, beforeBackup) || !bytes.Equal(local, beforeLocal) {
		t.Fatal("input mutated")
	}
	fresh, err := Restore(backup, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document(t, fresh), document(t, backup)) || bytes.Contains(fresh, []byte("private")) {
		t.Fatalf("fresh machine acquired secret: %s", fresh)
	}
}

func TestRestoreKeepsCredentialBearingIntegrationWhole(t *testing.T) {
	for _, section := range []string{"mcp_servers", "model_providers"} {
		local := []byte("[" + section + ".example]\nurl = 'https://local.example'\nname = 'local'\nhttp_headers = { Authorization = 'private' }\n")
		backup := []byte("[" + section + ".example]\nurl = 'https://other.example'\nname = 'remote'\n")
		got, err := Restore(backup, local)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(document(t, got), document(t, local)) {
			t.Fatalf("rebound local credential: %s", got)
		}
	}
	// Even a whole table-to-scalar change cannot erase local credentials.
	got, err := Restore([]byte("mcp_servers = 'changed'"), []byte("[mcp_servers.example.env]\nKEY = 'private'"))
	if err != nil || !bytes.Contains(got, []byte("private")) {
		t.Fatalf("type change dropped secret: %s %v", got, err)
	}
	// A credential-free integration can be updated normally.
	got, err = Restore([]byte("[mcp_servers.example]\nurl = 'https://new.example'"), []byte("[mcp_servers.example]\nurl = 'https://old.example'"))
	if err != nil || !bytes.Contains(got, []byte("https://new.example")) {
		t.Fatalf("safe update refused: %s %v", got, err)
	}
}

func TestRestoreDoesNotMatchArraySecretsByPosition(t *testing.T) {
	local := []byte(`items = [{ name = "first", password = "private" }, { name = "second" }]`)
	backup := []byte(`items = [{ name = "second" }, { name = "first" }]`)
	got, err := Restore(backup, local)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document(t, got), document(t, local)) {
		t.Fatalf("array secret moved: %s", got)
	}
}

func TestRestoreRefusesUnsanitizedBackups(t *testing.T) {
	for _, backup := range []string{`password = "private"`, `[mcp_servers.example.env]
KEY = "private"`, `items = [{ auth = "private" }]`} {
		got, err := Restore([]byte(backup), nil)
		if !errors.Is(err, ErrUnsafeBackup) || got != nil {
			t.Fatalf("accepted unsanitized backup: %v", err)
		}
	}
	got, err := Restore([]byte("unknown = 'sk-"+strings.Repeat("A1", 16)+"'"), nil)
	if !errors.Is(err, ErrSecret) || got != nil {
		t.Fatalf("accepted secret in backup: %v", err)
	}
}

func TestInvalidDocumentsAndLimits(t *testing.T) {
	for _, input := range []string{"broken = [private-value", "same = 1\nsame = 2", "x = '\xff'", "[x]\n[x]"} {
		got, err := Capture([]byte(input))
		if !errors.Is(err, ErrSyntax) || got != nil {
			t.Fatalf("accepted invalid TOML: %v", err)
		}
		if strings.Contains(err.Error(), "private-value") {
			t.Fatal("parser source leaked")
		}
		got, err = Restore([]byte("model = 'safe'"), []byte(input))
		if !errors.Is(err, ErrSyntax) || got != nil {
			t.Fatalf("overwrote invalid local document: %v", err)
		}
	}
	oversized := bytes.Repeat([]byte(" "), MaxBytes+1)
	if _, err := Capture(oversized); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	if _, err := Restore(nil, oversized); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	deep := []byte(strings.Repeat("level.", maxDepth+2) + "leaf = 1")
	if _, err := Capture(deep); !errors.Is(err, ErrDepth) {
		t.Fatal(err)
	}
	if _, err := Restore(nil, deep); !errors.Is(err, ErrDepth) {
		t.Fatal(err)
	}
	// Independently valid inputs may produce an oversized merged output.
	a := []byte("a = '" + strings.Repeat("a ", MaxBytes/3) + "'")
	b := []byte("b = '" + strings.Repeat("b ", MaxBytes/3) + "'")
	if got, err := Restore(a, b); !errors.Is(err, ErrSize) || got != nil {
		t.Fatalf("oversized result returned: %v", err)
	}
}

func FuzzCaptureRestore(f *testing.F) {
	for _, seed := range []string{"", "model = 'example'", "[env]\nKEY = 'private'", "items = [{ password = 'private' }]", "when = 2026-09-11"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		backup, err := Capture(source)
		if err != nil {
			if backup != nil {
				t.Fatal("partial output on error")
			}
			return
		}
		again, err := Capture(backup)
		if err != nil || !bytes.Equal(again, backup) {
			t.Fatalf("capture not idempotent: %v", err)
		}
		fresh, err := Restore(backup, nil)
		if err != nil || !bytes.Equal(fresh, backup) {
			t.Fatalf("fresh restore changed backup: %v", err)
		}
	})
}

func TestEnvironmentHeaderReferencesAreScoped(t *testing.T) {
	for _, source := range []string{
		"[mcp_servers.example.env_http_headers]\nAuthorization = 1234",
		"[model_providers.example.env_http_headers]\nAuthorization = 'not an env name'",
	} {
		if _, err := Capture([]byte(source)); !errors.Is(err, ErrSyntax) {
			t.Fatalf("invalid reference accepted: %v", err)
		}
	}
	// An unknown similarly named section cannot exempt credential-bearing fields.
	got, err := Capture([]byte("[unknown.env_http_headers]\nAuthorization = 'private'"))
	if err != nil || bytes.Contains(got, []byte("private")) {
		t.Fatalf("reference exemption escaped its scope: %s %v", got, err)
	}
}

func TestURLCredentialsCannotHideInUnknownValues(t *testing.T) {
	for _, value := range []string{"https://user:short@example.com/v1", "https://example.com?code=short", "prefix https://example.com/#short suffix", "https://user%3Ashort@example.com"} {
		source := []byte("unknown = '" + value + "'")
		if got, err := Capture(source); !errors.Is(err, ErrSecret) || got != nil {
			t.Fatalf("URL credential copied: %v", err)
		}
		restored, err := Restore([]byte("unknown = 'https://other.example'"), source)
		if err != nil || !bytes.Contains(restored, []byte(value)) {
			t.Fatalf("local credential URL lost: %v", err)
		}
	}
}

func TestLimitsBeforeDecoderRecursion(t *testing.T) {
	for _, source := range []string{
		"value = " + strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000),
		strings.Repeat("key.", 10000) + "value = 0",
		"[" + strings.Repeat("key.", 10000) + "value]\nx = 0",
	} {
		if _, err := Capture([]byte(source)); !errors.Is(err, ErrDepth) {
			t.Fatalf("unbounded decoder input: %v", err)
		}
	}
	// Brackets/dots in comments and every string form are content, not nesting.
	for _, quote := range []string{"'", "\"", "'''", "\"\"\""} {
		source := "# " + strings.Repeat("[.", 100) + "\nvalue = " + quote + strings.Repeat("[.", 100) + quote
		if _, err := Capture([]byte(source)); err != nil {
			t.Fatalf("string content counted as nesting: %v", err)
		}
	}
	for _, ending := range []string{"''''", "'''''", "\"\"\"\"", "\"\"\"\"\""} {
		opening := ending[:3]
		source := "value = " + opening + "text" + ending + "\narray = " + strings.Repeat("[", 100) + "0" + strings.Repeat("]", 100)
		if _, err := Capture([]byte(source)); !errors.Is(err, ErrDepth) {
			t.Fatalf("closing quotes hid recursion: %v", err)
		}
	}
}
