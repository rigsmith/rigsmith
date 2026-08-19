package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSessionKeychain replaces the platform Keychain hooks with an in-memory
// map keyed by config dir. Returns a record of seed-write targets. The write
// hook mirrors the darwin contract: update an existing entry only.
func stubSessionKeychain(t *testing.T, entries map[string][]byte) *[]string {
	t.Helper()
	var writes []string
	oldRead, oldWrite := sessionKeychainRead, sessionKeychainWrite
	sessionKeychainRead = func(dir string) ([]byte, bool, error) {
		raw, ok := entries[dir]
		return raw, ok, nil
	}
	sessionKeychainWrite = func(dir string, raw []byte) (bool, error) {
		if _, ok := entries[dir]; !ok {
			return false, nil
		}
		entries[dir] = raw
		writes = append(writes, dir)
		return true, nil
	}
	t.Cleanup(func() { sessionKeychainRead, sessionKeychainWrite = oldRead, oldWrite })
	return &writes
}

func TestHasTokens(t *testing.T) {
	for blob, want := range map[string]bool{
		string(sampleBlob("x", "max")):                                                  true,
		`{"claudeAiOauth":{"accessToken":"a"}}`:                                         true,
		`{"claudeAiOauth":{"refreshToken":"r"}}`:                                        true,
		`{"claudeAiOauth":{"accessToken":"","refreshToken":""},"organizationUuid":"o"}`: false,
		`{"claudeAiOauth":{}}`:                                                          false,
		`not json`:                                                                      false,
	} {
		if got := hasTokens([]byte(blob)); got != want {
			t.Errorf("hasTokens(%s) = %v, want %v", blob, got, want)
		}
	}
}

func TestSaveCredentialRefusesTokenless(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	// The exact blob Claude Code leaves behind when a login expires or logs out.
	stub := `{"claudeAiOauth":{"accessToken":"","refreshToken":"","subscriptionType":"max"},"organizationUuid":"org-w"}`
	if err := st.SaveCredential(a.ID, []byte(stub)); err == nil {
		t.Fatal("token-less blob should be refused")
	}
	raw, _ := st.Credential(a.ID)
	if string(raw) != string(sampleBlob("w", "max")) {
		t.Error("stored credential must be untouched after a refused save")
	}
	if err := st.SaveCredential(a.ID, sampleBlob("w2", "max")); err != nil {
		t.Fatalf("valid blob refused: %v", err)
	}
}

// A profile Claude Code has migrated to the Keychain (token-less file stub,
// tokens in the entry) is usable as-is; a stale reseed must reach the entry.
func TestEnsureSessionKeychainMigratedProfile(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	dir := st.ConfigDir(a.ID)
	entries := map[string][]byte{dir: sampleBlob("kc", "max")}
	writes := stubSessionKeychain(t, entries)

	mustWrite(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":""}}`)
	if _, err := st.EnsureSession(a, false, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(*writes) != 0 {
		t.Error("a usable, non-stale profile must not be reseeded")
	}

	// A store update marks the profile stale → the reseed must update the
	// Keychain entry, not just the file stub Claude Code no longer reads.
	if _, _, err := st.CaptureLive(sampleBlob("w2", "max"), sampleOAuth("w@x.com")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureSession(a, false, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if string(entries[dir]) != string(sampleBlob("w2", "max")) {
		t.Error("stale reseed should update the existing Keychain entry")
	}
	if fileExists(st.stalePath(a.ID)) {
		t.Error("stale marker should be cleared after the reseed")
	}
}

// A token-less STORED credential (an expired live login round-tripped over it,
// pre-guard) must never be seeded over a session that still authenticates —
// and must fail loudly when there is no healthy session to fall back on.
func TestEnsureSessionTokenlessStore(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	dir := st.ConfigDir(a.ID)
	entries := map[string][]byte{dir: sampleBlob("kc", "max")}
	stubSessionKeychain(t, entries)

	mustWrite(t, st.credPath(a.ID), `{"claudeAiOauth":{"accessToken":"","refreshToken":""}}`)
	mustWrite(t, st.stalePath(a.ID), "credential updated\n")
	if _, err := st.EnsureSession(a, false, t.TempDir()); err != nil {
		t.Fatalf("a healthy session must survive a dead store: %v", err)
	}
	if string(entries[dir]) != string(sampleBlob("kc", "max")) {
		t.Error("a dead store credential must not be seeded over a live session")
	}
	if fileExists(st.stalePath(a.ID)) {
		t.Error("stale marker should be cleared after the skip")
	}

	delete(entries, dir)
	_, err := st.EnsureSession(a, false, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no OAuth token") {
		t.Errorf("dead store + no session should refuse, got %v", err)
	}
}

func TestCaptureFromSession(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("old", "pro"), sampleOAuth("w@x.com"))
	dir := st.ConfigDir(a.ID)
	entries := map[string][]byte{dir: sampleBlob("fresh", "max")}
	stubSessionKeychain(t, entries)
	mustWrite(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{}}`)

	if err := st.CaptureFromSession(a); err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.Credential(a.ID); string(raw) != string(sampleBlob("fresh", "max")) {
		t.Error("stored credential should now be the session's Keychain tokens")
	}
	if fileExists(st.stalePath(a.ID)) {
		t.Error("capturing FROM the session must not mark it stale")
	}
	if got, _ := st.read(a.ID); got.SubscriptionType != "max" {
		t.Errorf("subscription should refresh from the captured blob, got %q", got.SubscriptionType)
	}

	// No Keychain entry → the file is the fallback (the off-macOS layout).
	delete(entries, dir)
	mustWrite(t, filepath.Join(dir, ".credentials.json"), string(sampleBlob("file", "pro")))
	if err := st.CaptureFromSession(a); err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.Credential(a.ID); string(raw) != string(sampleBlob("file", "pro")) {
		t.Error("file fallback not captured")
	}

	// Nothing usable anywhere → a clear error, store untouched.
	mustWrite(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{}}`)
	if err := st.CaptureFromSession(a); err == nil || !strings.Contains(err.Error(), "no usable session credential") {
		t.Errorf("want no-usable-session error, got %v", err)
	}
	if raw, _ := st.Credential(a.ID); string(raw) != string(sampleBlob("file", "pro")) {
		t.Error("failed capture must leave the store untouched")
	}
}

func TestStoredStatuses(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("a", "max"), sampleOAuth("a@x.com"))
	b, _, _ := st.CaptureLive(sampleBlob("b", "pro"), sampleOAuth("b@x.com"))
	if err := st.SetActive(a.ID); err != nil {
		t.Fatal(err)
	}
	stubSessionKeychain(t, map[string][]byte{})
	// b: session profile holding only a token-less stub, and a poisoned store
	// (written directly — SaveCredential would refuse it).
	mustWrite(t, filepath.Join(st.ConfigDir(b.ID), ".credentials.json"), `{"claudeAiOauth":{}}`)
	mustWrite(t, st.credPath(b.ID), `{"claudeAiOauth":{}}`)

	got, err := st.StoredStatuses()
	if err != nil || len(got) != 2 {
		t.Fatalf("StoredStatuses = %v, %v", got, err)
	}
	if !got[0].Active || !got[0].CredentialTokens || got[0].Session != SessionNone {
		t.Errorf("a@x.com status = %+v", got[0])
	}
	if got[1].Active || got[1].CredentialTokens || got[1].Session != SessionNoTokens {
		t.Errorf("b@x.com status = %+v", got[1])
	}

	// A session that CAN authenticate reports ok.
	os.Remove(st.credPath(b.ID))
	mustWrite(t, st.credPath(b.ID), string(sampleBlob("b", "pro")))
	mustWrite(t, filepath.Join(st.ConfigDir(b.ID), ".credentials.json"), string(sampleBlob("live", "pro")))
	got, _ = st.StoredStatuses()
	if !got[1].CredentialTokens || got[1].Session != SessionOK {
		t.Errorf("healthy b@x.com status = %+v", got[1])
	}
}
