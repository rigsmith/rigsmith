package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDesktop builds a Desktop data dir: a config.json with the given top-level
// keys and, when cookies is true, a Cookies DB shaped like Chromium's with a
// BLOB column — the part a naive text round-trip would corrupt.
func fakeDesktop(t *testing.T, keys map[string]string, cookies bool) string {
	t.Helper()
	root := t.TempDir()
	doc := map[string]any{"locale": "en-US", "userThemeMode": "dark"}
	for k, v := range keys {
		doc[k] = v
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if cookies {
		requireSQLiteOrSkip(t)
		db := filepath.Join(root, "Cookies")
		if _, err := runSQLite(db,
			"CREATE TABLE cookies (creation_utc INTEGER PRIMARY KEY, host_key TEXT, name TEXT, encrypted_value BLOB, path TEXT);",
			"INSERT INTO cookies VALUES (1,'.claude.ai','sessionKey',X'763130deadbeef00ff','/');",
			"INSERT INTO cookies VALUES (2,'.claude.ai','lastActiveOrg',X'76313000112233','/');",
			"INSERT INTO cookies VALUES (3,'.example.com','unrelated',X'aabb','/');",
		); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// enableDesktopForTest exercises the mechanics on any OS. The platform gate is a
// deployment policy — only macOS has been verified end to end — but the config
// and cookie handling underneath it is ordinary file and SQL work worth testing
// on every runner, not just the macOS one.
func enableDesktopForTest(t *testing.T) {
	t.Helper()
	orig := desktopSupported
	desktopSupported = true
	t.Cleanup(func() { desktopSupported = orig })
}

func requireSQLiteOrSkip(t *testing.T) {
	t.Helper()
	if requireSQLite() != nil {
		t.Skipf("%s not present", sqlite3Bin)
	}
}

func readConfigDoc(t *testing.T, root string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The core promise: capture then apply reproduces the session exactly, including
// the encrypted cookie blob, and without ever decrypting anything.
func TestDesktop_CaptureApplyRoundTrip(t *testing.T) {
	enableDesktopForTest(t)
	src := fakeDesktop(t, map[string]string{
		"oauth:tokenCache":     "djEwAAAA1111",
		"oauth:tokenCacheV2":   "djEwBBBB2222",
		"lastKnownAccountUuid": "03d1c0c9-823d-464b-a468-a9bea2383338",
	}, true)

	snap, err := CaptureDesktop(src)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.HasSession() {
		t.Fatal("captured snapshot should report a session")
	}
	if len(snap.CookieSQL) != 2 {
		t.Fatalf("want 2 claude.ai cookie rows, got %d (%v)", len(snap.CookieSQL), snap.CookieSQL)
	}
	for _, s := range snap.CookieSQL {
		if strings.Contains(s, "unrelated") {
			t.Error("cookies for other hosts must not be captured")
		}
	}

	// A second Desktop signed in as a different account.
	dst := fakeDesktop(t, map[string]string{
		"oauth:tokenCache":     "djEwOTHER9999",
		"oauth:tokenCacheV2":   "djEwOTHER8888",
		"lastKnownAccountUuid": "456fc32e-7579-49c7-bb2a-099657892c6a",
	}, true)
	if _, err := runSQLite(filepath.Join(dst, "Cookies"),
		"UPDATE cookies SET encrypted_value=X'999999' WHERE host_key LIKE '%claude.ai';"); err != nil {
		t.Fatal(err)
	}

	if err := applyForTest(dst, snap); err != nil {
		t.Fatal(err)
	}

	got := readConfigDoc(t, dst)
	for k, want := range map[string]string{
		"oauth:tokenCache":     "djEwAAAA1111",
		"oauth:tokenCacheV2":   "djEwBBBB2222",
		"lastKnownAccountUuid": "03d1c0c9-823d-464b-a468-a9bea2383338",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
	// Unmanaged keys must survive untouched.
	if got["locale"] != "en-US" || got["userThemeMode"] != "dark" {
		t.Errorf("unmanaged keys were disturbed: %v", got)
	}
	// The blob must come back byte-identical, and the other host's row untouched.
	out, err := runSQLite(filepath.Join(dst, "Cookies"),
		"SELECT name||'='||quote(encrypted_value) FROM cookies ORDER BY name;")
	if err != nil {
		t.Fatal(err)
	}
	// quote() renders blobs with uppercase hex, so compare case-insensitively.
	lower := strings.ToLower(out)
	for _, want := range []string{
		"sessionkey=x'763130deadbeef00ff'", // the encrypted value, byte-identical
		"lastactiveorg=x'76313000112233'",
		"unrelated=x'aabb'", // another host's row, untouched
	} {
		if !strings.Contains(lower, want) {
			t.Errorf("missing %q in restored cookies:\n%s", want, out)
		}
	}
	if strings.Contains(out, "999999") {
		t.Error("the displaced account's cookie value should have been replaced")
	}
}

// The subtle one. If the incoming account has no V1 entry, the outgoing
// account's must be REMOVED — leaving it behind would authenticate part of the
// app as the account just switched away from.
func TestDesktop_ApplyRemovesStaleManagedKeys(t *testing.T) {
	enableDesktopForTest(t)
	src := fakeDesktop(t, map[string]string{"oauth:tokenCacheV2": "djEwONLYV2"}, false)
	snap, err := CaptureDesktop(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := fakeDesktop(t, map[string]string{
		"oauth:tokenCache":     "djEwSTALE-V1",
		"oauth:tokenCacheV2":   "djEwSTALE-V2",
		"lastKnownAccountUuid": "old-uuid",
	}, false)

	if err := applyForTest(dst, snap); err != nil {
		t.Fatal(err)
	}
	got := readConfigDoc(t, dst)
	if _, present := got["oauth:tokenCache"]; present {
		t.Error("stale V1 token cache should have been removed, not left behind")
	}
	if _, present := got["lastKnownAccountUuid"]; present {
		t.Error("stale account uuid should have been removed")
	}
	if got["oauth:tokenCacheV2"] != "djEwONLYV2" {
		t.Errorf("V2 = %v, want the incoming value", got["oauth:tokenCacheV2"])
	}
}

// A snapshot is sealed with its machine's keychain key, so applying one from
// another host would write undecryptable bytes and look like a mystery logout.
// Refuse with an explanation instead.
func TestDesktop_RefusesSnapshotFromAnotherMachine(t *testing.T) {
	enableDesktopForTest(t)
	root := fakeDesktop(t, map[string]string{"oauth:tokenCacheV2": "djEwX"}, false)
	snap, err := CaptureDesktop(root)
	if err != nil {
		t.Fatal(err)
	}
	snap.Host = "some-other-machine"

	err = ApplyDesktop(root, snap)
	if err == nil {
		t.Fatal("applying a foreign snapshot should be refused")
	}
	if !strings.Contains(err.Error(), "some-other-machine") {
		t.Errorf("error should name the capturing machine: %v", err)
	}
}

func TestDesktop_HasSession(t *testing.T) {
	var nilSnap *DesktopSnapshot
	if nilSnap.HasSession() {
		t.Error("nil snapshot has no session")
	}
	empty := &DesktopSnapshot{ConfigKeys: map[string]json.RawMessage{}}
	if empty.HasSession() {
		t.Error("empty snapshot has no session")
	}
	// Identity alone is not a session — a logged-out Desktop still knows its
	// last account, and restoring that over a live login would be a downgrade.
	idOnly := &DesktopSnapshot{ConfigKeys: map[string]json.RawMessage{
		"lastKnownAccountUuid": json.RawMessage(`"abc"`),
	}}
	if idOnly.HasSession() {
		t.Error("lastKnownAccountUuid alone is not a session")
	}
}

func TestDesktop_CaptureMissingRootIsTyped(t *testing.T) {
	enableDesktopForTest(t)
	if _, err := CaptureDesktop(filepath.Join(t.TempDir(), "nope")); err != ErrNoDesktop {
		t.Errorf("err = %v, want ErrNoDesktop", err)
	}
}

func TestStore_SaveAndReadDesktop(t *testing.T) {
	enableDesktopForTest(t)
	st := &Store{Root: t.TempDir()}
	if got, err := st.Desktop("nobody"); err != nil || got != nil {
		t.Fatalf("absent snapshot = (%v, %v), want (nil, nil)", got, err)
	}
	root := fakeDesktop(t, map[string]string{"oauth:tokenCacheV2": "djEwZZZ"}, false)
	snap, err := CaptureDesktop(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDesktop("acct", snap); err != nil {
		t.Fatal(err)
	}
	back, err := st.Desktop("acct")
	if err != nil {
		t.Fatal(err)
	}
	if !back.HasSession() || string(back.ConfigKeys["oauth:tokenCacheV2"]) != `"djEwZZZ"` {
		t.Errorf("round-trip lost the session: %+v", back)
	}
	// It is a session, ciphertext or not, so it is written owner-only. Windows
	// does not carry Unix permission bits — the file reports 0666 there whatever
	// mode is requested — so only the behaviour, not the assertion, is portable.
	fi, err := os.Stat(st.desktopPath("acct"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not carry Unix permission bits")
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot mode = %o, want 600", perm)
	}
}

// applyForTest exercises the write path without the running-Desktop guard, which
// depends on real processes.
func applyForTest(root string, snap *DesktopSnapshot) error {
	if err := writeDesktopConfigKeys(root, snap.ConfigKeys); err != nil {
		return err
	}
	return importDesktopCookies(desktopCookiesPath(root), snap.CookieSQL)
}

// The CLI and Desktop are independent logins and routinely disagree. A snapshot
// has to say whose session it is, so a caller can refuse to file Desktop
// account B's session under CLI account A.
func TestDesktop_AccountUUIDIdentifiesTheSession(t *testing.T) {
	enableDesktopForTest(t)
	root := fakeDesktop(t, map[string]string{
		"oauth:tokenCacheV2":   "djEwX",
		"lastKnownAccountUuid": "03d1c0c9-823d-464b-a468-a9bea2383338",
	}, false)
	snap, err := CaptureDesktop(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.AccountUUID(); got != "03d1c0c9-823d-464b-a468-a9bea2383338" {
		t.Errorf("AccountUUID() = %q", got)
	}
	// The CLI half of the comparison reads the same uuid out of an oauthAccount
	// block, so a mismatch is detectable without decrypting anything.
	block := []byte(`{"emailAddress":"a@b.c","accountUuid":"456fc32e-7579-49c7-bb2a-099657892c6a"}`)
	if ProfileAccountUUID(block) == snap.AccountUUID() {
		t.Error("a different account's block must not compare equal")
	}
	same := []byte(`{"accountUuid":"03d1c0c9-823d-464b-a468-a9bea2383338"}`)
	if ProfileAccountUUID(same) != snap.AccountUUID() {
		t.Error("the same account's block must compare equal")
	}

	// A Desktop that never recorded an account yields "", which callers must
	// treat as unverified rather than as a match.
	noID := fakeDesktop(t, map[string]string{"oauth:tokenCacheV2": "djEwX"}, false)
	bare, err := CaptureDesktop(noID)
	if err != nil {
		t.Fatal(err)
	}
	if bare.AccountUUID() != "" {
		t.Error("absent lastKnownAccountUuid should yield an empty uuid")
	}
	if ProfileAccountUUID(nil) != "" {
		t.Error("an absent block should yield an empty uuid")
	}
}

func TestMatchDesktopAccount(t *testing.T) {
	const a = "03d1c0c9-823d-464b-a468-a9bea2383338"
	const b = "456fc32e-7579-49c7-bb2a-099657892c6a"
	withUUID := func(u string) *DesktopSnapshot {
		s := &DesktopSnapshot{ConfigKeys: map[string]json.RawMessage{}}
		if u != "" {
			s.ConfigKeys["lastKnownAccountUuid"] = json.RawMessage(`"` + u + `"`)
		}
		return s
	}
	block := func(u string) []byte { return []byte(`{"accountUuid":"` + u + `"}`) }

	cases := []struct {
		name string
		snap *DesktopSnapshot
		blk  []byte
		want DesktopMatch
	}{
		{"same account", withUUID(a), block(a), DesktopSame},
		{"desktop signed in as another account", withUUID(a), block(b), DesktopDifferent},
		{"desktop has no uuid", withUUID(""), block(a), DesktopUnknown},
		{"cli block has no uuid", withUUID(a), []byte(`{}`), DesktopUnknown},
		{"neither side known", withUUID(""), nil, DesktopUnknown},
		{"nil snapshot", nil, block(a), DesktopUnknown},
	}
	for _, c := range cases {
		if got := MatchDesktopAccount(c.snap, c.blk); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Writing under a running Desktop is silently lost, so it must be refused.
// Stubbed rather than probing for a real process: an assertion whose result
// depends on whether the developer has the app open is not an assertion.
func TestDesktop_RefusesWhileDesktopRunning(t *testing.T) {
	enableDesktopForTest(t)
	root := fakeDesktop(t, map[string]string{"oauth:tokenCacheV2": "djEwX"}, false)
	snap, err := CaptureDesktop(root)
	if err != nil {
		t.Fatal(err)
	}
	orig := desktopRunning
	t.Cleanup(func() { desktopRunning = orig })

	desktopRunning = func() bool { return true }
	if err := ApplyDesktop(root, snap); err != ErrDesktopRunning {
		t.Errorf("err = %v, want ErrDesktopRunning", err)
	}
	desktopRunning = func() bool { return false }
	if err := ApplyDesktop(root, snap); err != nil {
		t.Errorf("should apply when Desktop is closed: %v", err)
	}
}

// Cookie scoping must be the apex host or a dot-delimited subdomain. A bare
// `LIKE '%claude.ai'` also matches `notclaude.ai`, whose cookies are neither
// ours to carry nor ours to delete.
func TestDesktop_CookieScopeExcludesLookalikeHosts(t *testing.T) {
	enableDesktopForTest(t)
	requireSQLiteOrSkip(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"oauth:tokenCacheV2":"djEwX"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(root, "Cookies")
	if _, err := runSQLite(db,
		"CREATE TABLE cookies (creation_utc INTEGER PRIMARY KEY, host_key TEXT, name TEXT, encrypted_value BLOB);",
		"INSERT INTO cookies VALUES (1,'claude.ai','apex',X'01');",
		"INSERT INTO cookies VALUES (2,'.claude.ai','sub',X'02');",
		"INSERT INTO cookies VALUES (3,'notclaude.ai','lookalike',X'03');",
		"INSERT INTO cookies VALUES (4,'evil-claude.ai','lookalike2',X'04');",
	); err != nil {
		t.Fatal(err)
	}
	snap, err := CaptureDesktop(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(snap.CookieSQL, "\n")
	if !strings.Contains(joined, "apex") || !strings.Contains(joined, "'sub'") {
		t.Errorf("claude.ai cookies should be captured: %s", joined)
	}
	if strings.Contains(joined, "lookalike") {
		t.Errorf("look-alike hosts must not be captured: %s", joined)
	}
	// And restoring must not delete them either.
	if err := importDesktopCookies(db, snap.CookieSQL); err != nil {
		t.Fatal(err)
	}
	out, err := runSQLite(db, "SELECT name FROM cookies ORDER BY name;")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lookalike", "lookalike2"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s should have survived the restore: %s", want, out)
		}
	}
}

// An incoming account with no claude.ai cookies must still displace the
// outgoing account's, or the web UI keeps authenticating as the account just
// switched away from.
func TestDesktop_EmptyCookieSetStillDisplacesTheOutgoingSession(t *testing.T) {
	requireSQLiteOrSkip(t)
	root := t.TempDir()
	db := filepath.Join(root, "Cookies")
	if _, err := runSQLite(db,
		"CREATE TABLE cookies (creation_utc INTEGER PRIMARY KEY, host_key TEXT, name TEXT, encrypted_value BLOB);",
		"INSERT INTO cookies VALUES (1,'.claude.ai','sessionKey',X'01');",
		"INSERT INTO cookies VALUES (2,'.example.com','other',X'02');",
	); err != nil {
		t.Fatal(err)
	}
	if err := importDesktopCookies(db, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSQLite(db, "SELECT name FROM cookies;")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sessionKey") {
		t.Error("the outgoing account's cookies must be cleared even with nothing to insert")
	}
	if !strings.Contains(out, "other") {
		t.Error("another host's cookies must be untouched")
	}
}

// Having cookies to restore and no database to restore them into is a failure,
// not a silent success — the caller would otherwise report a completed switch.
func TestDesktop_MissingCookieDBWithRowsIsAnError(t *testing.T) {
	err := importDesktopCookies(filepath.Join(t.TempDir(), "Cookies"),
		[]string{"INSERT INTO cookies (name) VALUES ('x');"})
	if err == nil {
		t.Error("restoring cookies with no database should fail loudly")
	}
	if err := importDesktopCookies(filepath.Join(t.TempDir(), "Cookies"), nil); err != nil {
		t.Errorf("nothing to restore and no database is not an error: %v", err)
	}
}

// Snapshots name their columns, so a Desktop update that ADDS a cookie column
// between capture and restore still replays.
func TestDesktop_CookieRestoreSurvivesAddedColumn(t *testing.T) {
	enableDesktopForTest(t)
	requireSQLiteOrSkip(t)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "config.json"), []byte(`{"oauth:tokenCacheV2":"djEwX"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runSQLite(filepath.Join(src, "Cookies"),
		"CREATE TABLE cookies (creation_utc INTEGER PRIMARY KEY, host_key TEXT, name TEXT, encrypted_value BLOB);",
		"INSERT INTO cookies VALUES (1,'.claude.ai','sessionKey',X'763130aa');",
	); err != nil {
		t.Fatal(err)
	}
	snap, err := CaptureDesktop(src)
	if err != nil {
		t.Fatal(err)
	}
	// The app updates; Chromium grows a column.
	dst := filepath.Join(t.TempDir(), "Cookies")
	if _, err := runSQLite(dst,
		"CREATE TABLE cookies (creation_utc INTEGER PRIMARY KEY, host_key TEXT, name TEXT, encrypted_value BLOB, source_scheme INTEGER DEFAULT 0);",
	); err != nil {
		t.Fatal(err)
	}
	if err := importDesktopCookies(dst, snap.CookieSQL); err != nil {
		t.Fatalf("a snapshot must replay across an added column: %v", err)
	}
	out, err := runSQLite(dst, "SELECT name||'='||quote(encrypted_value) FROM cookies;")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out), "sessionkey=x'763130aa'") {
		t.Errorf("value not restored intact: %s", out)
	}
}

// On a platform we have not verified, both entry points refuse explicitly rather
// than quietly recording nothing — a feature that silently captures no session
// looks identical to one with nothing to capture.
func TestDesktop_UnsupportedPlatformRefusesLoudly(t *testing.T) {
	orig := desktopSupported
	desktopSupported = false
	t.Cleanup(func() { desktopSupported = orig })

	root := t.TempDir()
	if _, err := CaptureDesktop(root); err != ErrDesktopUnsupported {
		t.Errorf("CaptureDesktop err = %v, want ErrDesktopUnsupported", err)
	}
	if err := ApplyDesktop(root, &DesktopSnapshot{}); err != ErrDesktopUnsupported {
		t.Errorf("ApplyDesktop err = %v, want ErrDesktopUnsupported", err)
	}
	if DesktopSupported() {
		t.Error("DesktopSupported() should report the gate")
	}
}
