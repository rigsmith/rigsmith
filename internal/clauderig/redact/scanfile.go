package redact

import (
	"bytes"
	"path"
	"regexp"
	"strings"
)

// Non-JSON tripwire.
//
// Scan/ScanBytes only see parsed JSON, so a secret-bearing file that isn't JSON
// rode the sync untouched — the case that motivated this was Claude Desktop's
// `.audit-key`, 51 bytes of raw binary sitting inside an allowed tree.
//
// The constraint that shapes everything here: a finding aborts the WHOLE sync
// (engine.Sync refuses to push on any finding). A false positive therefore does
// not merely add noise, it bricks syncing until someone intervenes by hand. So
// this file's rules are deliberately narrow and high-signal, and the generic
// entropy backstop that LooksSecret applies to JSON *values* is NOT applied to
// arbitrary file bytes:
//
//   - Transcripts are megabytes of pasted prose, code, diffs, base64 images and
//     lockfile hashes. Entropy-scanning them would fire constantly.
//   - The real `.audit-key` measured 3.92–4.03 bits/char — at or below
//     LooksSecret's own 4.0 threshold — so entropy would have missed it anyway
//     while still false-positiving on everything else. It is binary, not text.
//
// What actually identifies these files is their NAME. That is the primary rule
// below; content rules are a small, near-zero-false-positive supplement.

// scanContentLimit caps how much of a file is examined. Credential files are
// small by nature — a key, a PEM block, a dotenv — while the big non-JSON files
// under an allowed tree are transcripts, which are exactly what must not trip the
// wire. Skipping large files is a deliberate FP guard, not an optimisation.
const scanContentLimit = 64 << 10

// keyMaterialNames are basenames whose whole reason to exist is to hold a key.
// The name alone is conclusive — there is no benign `id_rsa`.
var keyMaterialNames = map[string]bool{
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	".audit-key": true,
}

// keyMaterialSuffixes mark a basename as key material. `-key`/`_key` are included
// because the shape that started this (`.audit-key`) uses a dash, not a dot.
var keyMaterialSuffixes = []string{
	".pem", ".key", "-key", "_key", ".p12", ".pfx",
	".keystore", ".jks", ".ppk", ".gpg",
}

// authConfigNames are files that MAY carry auth but usually don't: an .npmrc is
// normally just `registry=…`, an .env is normally just flags. Flagging these on
// the name alone was tried and immediately false-positived on the four vendored
// `.npmrc` files in the official plugin marketplace — which, because a finding
// aborts the whole sync, would have made clauderig unable to sync at all. They
// are therefore confirmed against their content before being reported.
var authConfigNames = map[string]bool{
	".netrc": true, ".npmrc": true, ".pgpass": true,
	".env": true, "credentials": true, "credentials.txt": true,
}

// splitAssign splits a config line into key and value. '=' wins over ':' when
// both are present, which is what makes the npmrc form work: in
// `//registry.npmjs.org/:_authToken=abc` the colon belongs to the key, not to the
// assignment, so splitting on the first ':' would yield the registry URL as the
// key and miss the token entirely. The key's own trailing ':' segment is then the
// scoped name (`_authToken`).
func splitAssign(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, '=')
	if i < 0 {
		if i = strings.IndexByte(line, ':'); i < 0 {
			return "", "", false
		}
	}
	key = strings.TrimSpace(line[:i])
	val = strings.TrimSpace(line[i+1:])
	if key == "" {
		return "", "", false
	}
	if j := strings.LastIndex(key, ":"); j >= 0 {
		key = key[j+1:]
	}
	return key, val, true
}

// netrcSecretRe catches the whitespace-separated netrc/pgpass shape, which has no
// assignment operator at all: `machine host login me password s3cret`.
var netrcSecretRe = regexp.MustCompile(`(?i)\bpassword\s+(\S+)`)

// placeholderRe marks values that are documentation, not credentials: empty,
// `<your-key>`, `${VAR}`, `...`, `xxxx`, `changeme`.
var placeholderRe = regexp.MustCompile(`(?i)^(|["']?)([<$].*|.*\.\.\..*|x{3,}|\*{3,}|changeme|your[-_ ]?\w*|replace[-_ ]?me|todo|none|null|true|false|\d+)(["']?)$`)

// hasAuthAssignment reports whether text contains an assignment whose KEY reads
// as a secret and whose VALUE is a real one. It reuses isSecretKey — the same
// vocabulary the JSON redactor uses — so the two halves of the tripwire agree on
// what "secret-looking" means instead of drifting apart.
func hasAuthAssignment(text string) bool {
	p := DefaultPolicy()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if key, val, ok := splitAssign(line); ok {
			// An unset key is a template waiting to be filled in, not a leak.
			if val != "" && isSecretKey(strings.ToLower(key), p) && !placeholderRe.MatchString(val) {
				return true
			}
			continue
		}
		if m := netrcSecretRe.FindStringSubmatch(line); m != nil && !placeholderRe.MatchString(m[1]) {
			return true
		}
	}
	return false
}

// notSecretSuffixes defuse the name rules: a documented sample is not a leak, and
// a public key is not secret. Checked before the rules above.
var notSecretSuffixes = []string{
	".example", ".sample", ".template", ".dist", ".md", ".pub",
}

// NameVerdict is how much a filename alone settles.
type NameVerdict int

const (
	NameOrdinary    NameVerdict = iota // nothing to see
	NameAuthConfig                     // may hold auth — confirm against content
	NameKeyMaterial                    // is a credential; content is irrelevant
)

// ClassifyName judges a path's BASENAME. Directory components are ignored — the
// rule is about the file, so a project legitimately named "keys/" does not make
// everything inside it trip.
func ClassifyName(rel string) NameVerdict {
	name := strings.ToLower(path.Base(path.Clean(strings.ReplaceAll(rel, "\\", "/"))))
	for _, s := range notSecretSuffixes {
		if strings.HasSuffix(name, s) {
			return NameOrdinary
		}
	}
	// A public key never trips, whatever else its name says.
	if strings.Contains(name, "public") {
		return NameOrdinary
	}
	if keyMaterialNames[name] {
		return NameKeyMaterial
	}
	for _, s := range keyMaterialSuffixes {
		if strings.HasSuffix(name, s) {
			return NameKeyMaterial
		}
	}
	// `.env.local`, `.env.production` — `.env.example` already returned Ordinary.
	if authConfigNames[name] || strings.HasPrefix(name, ".env.") {
		return NameAuthConfig
	}
	return NameOrdinary
}

// isBinary reports whether data looks binary — a NUL byte in the first block is
// the usual heuristic, and it is what keeps images and compiled artifacts out of
// the text rules below.
func isBinary(data []byte) bool {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// ScanFile is the tripwire for a file that is not JSON. rel is the file's path
// relative to its sync root (used for the name rules and reported back as the
// finding path); data is its content, which may be truncated by the caller.
//
// It reports at most one finding per file — the file either is credential
// material or it isn't, and repeating that per line would only inflate the count
// in the error message.
func ScanFile(rel string, data []byte) []Finding {
	verdict := ClassifyName(rel)
	if verdict == NameKeyMaterial {
		return []Finding{{Path: rel, Kind: "key-material"}}
	}
	if len(data) == 0 || len(data) > scanContentLimit || isBinary(data) {
		return nil
	}
	if verdict == NameAuthConfig && hasAuthAssignment(string(data)) {
		return []Finding{{Path: rel, Kind: "auth-config"}}
	}
	// A PEM private key block is unambiguous wherever it appears.
	if pemRe.Match(data) {
		return []Finding{{Path: rel, Kind: "private-key"}}
	}
	// The whole file being one opaque token is the other unambiguous shape: a
	// token dropped into a file on its own. Checked against the trimmed content so
	// a trailing newline doesn't defeat it. Multi-line content is prose, config or
	// code — not a bare token — so it is left alone.
	s := strings.TrimSpace(string(data))
	if s != "" && !strings.ContainsAny(s, "\n\r") {
		if kind, ok := LooksSecret(s); ok {
			return []Finding{{Path: rel, Kind: kind}}
		}
	}
	return nil
}

// ScanContentLimit is the size above which ScanFile examines nothing but the
// name, exported so callers can avoid reading more than the scan will use.
func ScanContentLimit() int { return scanContentLimit }
