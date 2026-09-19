package redact

import (
	"strings"
	"testing"
)

// body is one line of PEM base64, at the length a real key actually uses. The
// short stand-ins these tests used to carry ("abc", "MIIEow==") were the reason
// the length floor could not be raised without rewriting half the suite.
const body = "MIIEpAIBAAKCAQEAvJ8kL2mN4pQ6rS8tU0vW2xY4zA6bC8dE0fG2hI4jK6lM8nO0"

// The incident this rule exists for: a cached grep dump over a Nuxt project held
// minified sourcemaps of `jose` and `@octokit/auth-app`, which compare against
// the PEM header text. Twelve headers, no key — and because a private-key
// finding is File:true, the tripwire refused every sync until the file was
// deleted by hand.
func TestHasPrivateKeyMaterial_HeaderWithoutKeyIsNotKeyMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"octokit source", `return privateKey.includes("-----BEGIN RSA PRIVATE KEY-----");`},
		{"jose source", `if (pkcs8.indexOf('-----BEGIN PRIVATE KEY-----') !== 0) {`},
		{"sourcemap, escaped newlines around it", `{"sourcesContent":["export function isPkcs1(k) {\n  return k.includes(\"-----BEGIN RSA PRIVATE KEY-----\");\n}\n"]}`},
		{"prose", "it printed -----BEGIN OPENSSH PRIVATE KEY----- and then stopped"},
		{"header alone at end of file", "log output\n-----BEGIN RSA PRIVATE KEY-----\n"},
		{"docs showing a redacted block", "-----BEGIN PRIVATE KEY-----\n<your key here>\n-----END PRIVATE KEY-----"},
		{"markdown with an elided body", "-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----"},
		// The shape that took the second sync down: a task-suggestion prompt in
		// Desktop's session JSON, where the next thing after the header is an
		// ordinary long identifier a line or two below.
		{"header then prose containing a long identifier", "-----BEGIN RSA PRIVATE KEY-----\nthe field was backgroundTaskSuggestions[0].prompt\n"},
	} {
		if HasPrivateKeyMaterial([]byte(tc.in)) {
			t.Errorf("%s: flagged as key material, want not flagged: %q", tc.name, tc.in)
		}
	}
}

func TestHasPrivateKeyMaterial_RealKeysStillTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"plain block", "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----"},
		{"openssh", "-----BEGIN OPENSSH PRIVATE KEY-----\n" + body + "\n"},
		{"pkcs8", "-----BEGIN PRIVATE KEY-----\n" + body + "\n"},
		{"crlf line endings", "-----BEGIN RSA PRIVATE KEY-----\r\n" + body + "\r\n"},
		{"buried in a file", strings.Repeat("ordinary prose\n", 100) + "-----BEGIN RSA PRIVATE KEY-----\n" + body},
		// A key inside a JSON string: a transcript, a sourcemap, a .env — the
		// line breaks arrive as the two-character escape, not the byte.
		{"json-escaped", `{"key":"-----BEGIN RSA PRIVATE KEY-----\n` + body + `\n-----END RSA PRIVATE KEY-----"}`},
		{"json-escaped crlf", `{"key":"-----BEGIN RSA PRIVATE KEY-----\r\n` + body + `"}`},
		// A transcript records tool output that was itself JSON, so the same
		// break arrives doubled. Reading only the single form took this for a
		// header with nothing after it.
		{"json-escaped twice", `{"text":"it printed -----BEGIN RSA PRIVATE KEY-----\\n` + body + ` and was cut off"}`},
		{"json-escaped twice, crlf", `{"text":"-----BEGIN RSA PRIVATE KEY-----\\r\\n` + body + `"}`},
		// Environment variables hold keys on one line, space-separated.
		{"single line, space separated", "PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY----- " + body + " -----END RSA PRIVATE KEY-----"},
		// Encrypted legacy keys put RFC 1421 attributes between the header and
		// the body. These are the keys most worth catching, so the attribute
		// lines must not be mistaken for "no body follows".
		{"encrypted, rfc 1421 attributes", "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,8A7B6C5D4E3F2A1B\n\n" + body + "\n"},
		// The first header is a false positive and a real key follows it: the
		// scan must not stop at the first header it sees.
		{"quoted header before a real key", `k.includes("-----BEGIN RSA PRIVATE KEY-----")` + "\nlater:\n-----BEGIN RSA PRIVATE KEY-----\n" + body},
	} {
		if !HasPrivateKeyMaterial([]byte(tc.in)) {
			t.Errorf("%s: not flagged, want key material: %q", tc.name, tc.in)
		}
	}
}

// The line-at-a-time scrubbers cannot see the body — in raw text it is on the
// lines after the header — so for them the header alone still has to be enough.
// If this collapses into HasPrivateKeyMaterial, those callers stop refusing a
// key whose header is the only part they are looking at.
func TestHasPrivateKeyHeader_StaysHeaderOnly(t *testing.T) {
	header := []byte("-----BEGIN RSA PRIVATE KEY-----\n")
	if !HasPrivateKeyHeader(header) {
		t.Error("per-line guard must trip on the header alone")
	}
	if HasPrivateKeyMaterial(header) {
		t.Error("whole-content rule must NOT trip on the header alone — that is the bug")
	}
	if HasPrivateKeyHeader([]byte("no marker here")) {
		t.Error("per-line guard tripped without a header")
	}
}

// The three whole-content entry points have to agree: ScanFile for non-JSON
// files, ScanReader for streamed ones, LooksSecret for a single value. When they
// disagree the tool either refuses something it has already cleaned, or cleans
// something it will still refuse.
func TestPrivateKeyVerdictIsConsistentAcrossEntryPoints(t *testing.T) {
	quoted := `return k.includes("-----BEGIN RSA PRIVATE KEY-----");`
	real := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "\n-----END RSA PRIVATE KEY-----\n"

	if got := ScanFile("skills/s/notes.txt", []byte(quoted)); len(got) != 0 {
		t.Errorf("ScanFile flagged a quoted header: %+v", got)
	}
	if got := ScanFile("skills/s/notes.txt", []byte(real)); len(got) != 1 || got[0].Kind != "private-key" {
		t.Errorf("ScanFile missed a real key: %+v", got)
	}

	f, err := ScanReader("cli/projects/x/s.jsonl", strings.NewReader(quoted))
	if err != nil || f != nil {
		t.Errorf("ScanReader flagged a quoted header: %+v %v", f, err)
	}
	f, err = ScanReader("cli/projects/x/s.jsonl", strings.NewReader(real))
	if err != nil || f == nil || f.Kind != "private-key" {
		t.Errorf("ScanReader missed a real key: %+v %v", f, err)
	}

	if _, ok := LooksSecret(quoted); ok {
		t.Error("LooksSecret flagged a quoted header")
	}
	if kind, ok := LooksSecret(real); !ok || kind != "private-key" {
		t.Errorf("LooksSecret(real key) = (%q,%v), want private-key", kind, ok)
	}
}
