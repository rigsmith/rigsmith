package redact

import (
	"strings"
	"testing"
)

func TestLooksCredentialFile(t *testing.T) {
	secret := []string{
		"desktop/local-agent-mode-sessions/03d/e30/local_x/.audit-key",
		"skills/s/id_rsa",
		"skills/s/server.pem",
		"skills/s/signing.key",
		"skills/s/release_key",
		"a/store.p12",
		"a/keystore.jks",
		"UPPER/Signing.KEY", // matching is case-insensitive
	}
	for _, rel := range secret {
		if ClassifyName(rel) != NameKeyMaterial {
			t.Errorf("should be key material on name alone: %s", rel)
		}
	}

	// These may hold auth but usually don't, so the name only earns a content check.
	for _, rel := range []string{"projects/p/.env", "projects/p/.env.production", "a/.netrc", "a/.npmrc"} {
		if ClassifyName(rel) != NameAuthConfig {
			t.Errorf("should need content confirmation: %s", rel)
		}
	}

	safe := []string{
		"skills/s/SKILL.md",
		"projects/p/transcript.jsonl",
		"skills/s/.env.example",    // documented sample
		"skills/s/.env.sample",     //
		"skills/s/config.key.dist", // shipped default
		"skills/s/server.pub",      // public half
		"skills/s/public.key",      // ditto, by name
		"skills/s/monkey",          // "key" is a substring, not a suffix
		"skills/s/keyboard.md",
		"keys/notes.md", // a dir called keys must not taint its contents
		"a/b/README",
	}
	for _, rel := range safe {
		if ClassifyName(rel) != NameOrdinary {
			t.Errorf("should NOT be flagged: %s", rel)
		}
	}
}

// The file that motivated the whole check: 51 bytes of raw binary, entropy below
// LooksSecret's own threshold, caught on its name rather than its content.
func TestScanFile_AuditKeyBinary(t *testing.T) {
	binary := []byte("\x8f\x00\x1a\xd4tK\x91\x2c\xff\x03A\x7e\xb0\x11\x9e\x44")
	got := ScanFile("desktop/local-agent-mode-sessions/03d/e30/local_x/.audit-key", binary)
	if len(got) != 1 || got[0].Kind != "key-material" {
		t.Fatalf("ScanFile = %+v, want one key-material finding", got)
	}
	if got[0].Path == "" {
		t.Error("finding should carry the file path")
	}
}

func TestScanFile_ContentRules(t *testing.T) {
	pem := []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEow==\n-----END RSA PRIVATE KEY-----\n")
	if got := ScanFile("skills/s/notes.txt", pem); len(got) != 1 || got[0].Kind != "private-key" {
		t.Errorf("PEM block should trip wherever it appears: %+v", got)
	}
	// A bare token alone in a file, trailing newline and all.
	if got := ScanFile("skills/s/token.txt", []byte("sk-ant-api03-AAAABBBBCCCCDDDD\n")); len(got) != 1 {
		t.Errorf("a lone token should trip: %+v", got)
	}
	// One finding per file, not one per match.
	two := []byte("sk-ant-api03-AAAABBBBCCCCDDDD")
	if got := ScanFile("skills/s/token.txt", two); len(got) != 1 {
		t.Errorf("want exactly one finding, got %d", len(got))
	}
}

// An auth-config name is only a finding once its CONTENT confirms it. The benign
// case here is real: four vendored `.npmrc` files ship in the official plugin
// marketplace, and flagging them on name alone aborted every sync.
func TestScanFile_AuthConfigNeedsContent(t *testing.T) {
	quiet := map[string]string{
		".npmrc": "registry=https://registry.npmjs.org/\n",
		".env":   "DEBUG=true\nPORT=3000\nLOG_LEVEL=info\n",
		".netrc": "machine api.example.com login jdoe\n",
	}
	for name, body := range quiet {
		if got := ScanFile("plugins/marketplaces/x/"+name, []byte(body)); len(got) != 0 {
			t.Errorf("%s without auth should not trip: %+v", name, got)
		}
	}

	loud := map[string]string{
		".npmrc": "registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken=abc123def456\n",
		".env":   "DEBUG=true\nANTHROPIC_API_KEY=sk-ant-api03-realvalue\n",
		".netrc": "machine api.example.com login jdoe password hunter2\n",
	}
	for name, body := range loud {
		got := ScanFile("plugins/marketplaces/x/"+name, []byte(body))
		if len(got) != 1 || got[0].Kind != "auth-config" {
			t.Errorf("%s with auth should trip as auth-config: %+v", name, got)
		}
	}

	// Documented placeholders are not credentials.
	for _, body := range []string{
		"API_KEY=<your-key-here>\n",
		"API_TOKEN=${NPM_TOKEN}\n",
		"password=changeme\n",
		"AUTH_TOKEN=xxxxxxxx\n",
		"SECRET=\n",
	} {
		if got := ScanFile("skills/s/.env", []byte(body)); len(got) != 0 {
			t.Errorf("placeholder %q should not trip: %+v", body, got)
		}
	}
}

// The FP guards. Each of these would abort every sync if it tripped, so they
// matter more than the detections above.
func TestScanFile_DoesNotTripOnOrdinaryContent(t *testing.T) {
	cases := []struct{ name, body string }{
		{"prose", "This skill explains how to rotate an API key safely.\nUse the CLI.\n"},
		{"multiline config", "model: fable\neffort: high\ntoken_budget: 40000\n"},
		{"markdown with example key", "Set `ANTHROPIC_API_KEY=sk-ant-...` in your shell.\n"},
		{"git sha", "e42fc562972164d0318e2f6d93bca7a722c5f876\n"},
		{"path", "/Users/john/Library/Application Support/Claude/config.json\n"},
		{"sri digest", "sha512-oPjB4tGZ5vBLI0kbNb0jKX0mCFvXtQ0Zt1YAaFJ8kw2mCyq7yQ==\n"},
	}
	for _, c := range cases {
		if got := ScanFile("skills/s/SKILL.md", []byte(c.body)); len(got) != 0 {
			t.Errorf("%s should not trip the wire: %+v", c.name, got)
		}
	}
}

// A transcript is the highest-volume non-JSON file and the biggest FP risk: it is
// full of pasted base64, diffs and hashes. Anything over the content limit is
// judged on its name alone.
func TestScanFile_LargeFileScannedByNameOnly(t *testing.T) {
	huge := []byte("-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("A1b2C3d4", 20000))
	if got := ScanFile("projects/p/transcript.jsonl", huge); len(got) != 0 {
		t.Errorf("large file should be skipped by content: %+v", got)
	}
	// ...but a large file whose NAME is credential material still trips.
	if got := ScanFile("projects/p/id_rsa", huge); len(got) != 1 {
		t.Errorf("name rule must apply regardless of size: %+v", got)
	}
}

func TestScanFile_BinaryIsNotEntropyScanned(t *testing.T) {
	// A PNG-ish blob under a skill: high entropy, contains NUL — must stay quiet.
	blob := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), []byte(strings.Repeat("\x00\xff\xa3\x17", 500))...)
	if got := ScanFile("skills/s/logo.png", blob); len(got) != 0 {
		t.Errorf("binary asset should not trip: %+v", got)
	}
}

// `public` defuses the name rules as a WORD, not a substring: `notpublic.key`
// is key material and must still trip, or a binary file with that name would
// escape both the name rule and (being binary) the content rules.
func TestClassifyName_PublicIsAWordNotASubstring(t *testing.T) {
	for _, rel := range []string{
		"skills/s/public.key",
		"skills/s/server-public.pem",
		"skills/s/id_rsa_public",
	} {
		if ClassifyName(rel) != NameOrdinary {
			t.Errorf("a public key should not trip: %s", rel)
		}
	}
	for _, rel := range []string{
		"skills/s/notpublic.key",
		"skills/s/republic.pem",
	} {
		if ClassifyName(rel) != NameKeyMaterial {
			t.Errorf("%q merely CONTAINS \"public\" — it is still key material", rel)
		}
	}
}

// .pgpass carries its password in a colon-delimited record with no assignment
// operator, so the generic key=value pass never sees it.
func TestScanFile_PgpassPassword(t *testing.T) {
	loud := "myhost:5432:mydb:jdoe:s3cretpw\n"
	got := ScanFile("home/.pgpass", []byte(loud))
	if len(got) != 1 || got[0].Kind != "auth-config" {
		t.Errorf("a pgpass password should trip: %+v", got)
	}
	// Escaped colons belong to the field, not the delimiter — the password here
	// is still the last field.
	esc := `my\:host:5432:my\:db:jdoe:pw\:withcolon` + "\n"
	if got := ScanFile("home/.pgpass", []byte(esc)); len(got) != 1 {
		t.Errorf("escaped colons should not defeat the parse: %+v", got)
	}
	for _, quiet := range []string{
		"# comment only\n",
		"myhost:5432:mydb:jdoe:\n",         // no password set
		"myhost:5432:mydb:jdoe:changeme\n", // placeholder
		"key: value\n",                     // too few fields to be pgpass
	} {
		if got := ScanFile("home/.pgpass", []byte(quiet)); len(got) != 0 {
			t.Errorf("%q should not trip: %+v", quiet, got)
		}
	}
}
