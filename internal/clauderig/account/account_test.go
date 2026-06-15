package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleBlob builds a credential blob with a given refresh token and subscription.
func sampleBlob(refresh, sub string) []byte {
	m := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "acc-" + refresh,
			"refreshToken":     refresh,
			"expiresAt":        int64(1781560281239),
			"subscriptionType": sub,
		},
		"organizationUuid": "org-" + refresh,
	}
	b, _ := json.Marshal(m)
	return b
}

func TestParseBlobFingerprintStable(t *testing.T) {
	a1, err := parseBlob(sampleBlob("tokenA", "max"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := parseBlob(sampleBlob("tokenA", "max"))
	if a1.ID != a2.ID {
		t.Fatalf("same token -> different id: %s vs %s", a1.ID, a2.ID)
	}
	b, _ := parseBlob(sampleBlob("tokenB", "max"))
	if a1.ID == b.ID {
		t.Fatal("different tokens collided to same id")
	}
	if a1.SubscriptionType != "max" || a1.OrganizationUUID != "org-tokenA" {
		t.Fatalf("metadata not parsed: %+v", a1)
	}
}

func TestParseBlobNoToken(t *testing.T) {
	if _, err := parseBlob([]byte(`{"claudeAiOauth":{}}`)); err == nil {
		t.Fatal("expected error for tokenless blob")
	}
	if _, err := parseBlob([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestSaveListResolve(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, err := AccountFromBlob(sampleBlob("work-token", "max"), "work", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.AddedAt == "" {
		t.Fatal("AddedAt not stamped")
	}
	if err := st.Save(a, sampleBlob("work-token", "max")); err != nil {
		t.Fatal(err)
	}

	list, err := st.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}

	// Resolve by exact id, label, and unambiguous prefix.
	for _, ref := range []string{a.ID, "work", a.ID[:4]} {
		got, err := st.Resolve(ref)
		if err != nil || got.ID != a.ID {
			t.Fatalf("Resolve(%q) = %+v, %v", ref, got, err)
		}
	}
	if _, err := st.Resolve("nope"); err == nil {
		t.Fatal("expected miss for unknown ref")
	}

	// Credential round-trips and is 0600.
	raw, err := st.Credential(a.ID)
	if err != nil || len(raw) == 0 {
		t.Fatalf("Credential = %v, %v", raw, err)
	}
	fi, _ := os.Stat(filepath.Join(st.acctDir(a.ID), "credentials.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("credential perms = %v, want 0600", fi.Mode().Perm())
	}
}

func TestResolveEmptyStore(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if _, err := st.Resolve("anything"); err == nil {
		t.Fatal("expected error when no accounts exist")
	}
}

func TestNextRotation(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	mk := func(tok, label string) Account {
		a, _ := AccountFromBlob(sampleBlob(tok, "max"), label, time.Now())
		if err := st.Save(a, sampleBlob(tok, "max")); err != nil {
			t.Fatal(err)
		}
		return a
	}
	mk("t1", "aaa")
	mk("t2", "bbb")
	mk("t3", "ccc")

	list, _ := st.List() // sorted by label: aaa, bbb, ccc
	first, second := list[0], list[1]

	next, err := st.Next(first.ID)
	if err != nil || next.ID != second.ID {
		t.Fatalf("Next after first = %+v, want %s", next, second.ID)
	}
	// Wrap-around from last.
	last := list[len(list)-1]
	wrap, _ := st.Next(last.ID)
	if wrap.ID != first.ID {
		t.Fatalf("Next after last = %s, want wrap to %s", wrap.ID, first.ID)
	}
	// Untracked live id -> first.
	none, _ := st.Next("ffffffff")
	if none.ID != first.ID {
		t.Fatalf("Next(untracked) = %s, want %s", none.ID, first.ID)
	}
}

func TestPrepareSessionSharesAndIsolates(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	home := t.TempDir() // stand-in for ~/.claude
	// Default profile has some customizations and a credential + history that
	// must NOT leak into the session.
	mustWrite(t, filepath.Join(home, "settings.json"), `{"theme":"dark"}`)
	mustWrite(t, filepath.Join(home, "CLAUDE.md"), "rules")
	if err := os.MkdirAll(filepath.Join(home, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, "skills", "x.md"), "skill")
	mustWrite(t, filepath.Join(home, ".credentials.json"), `{"DEFAULT":true}`)

	a, _ := AccountFromBlob(sampleBlob("sess-token", "max"), "work", time.Now())
	raw := sampleBlob("sess-token", "max")

	dir, err := st.PrepareSession(a, raw, true, home)
	if err != nil {
		t.Fatal(err)
	}

	// The session credential is the account's, not the default profile's.
	got, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(got) != string(raw) {
		t.Fatalf("session credential not the account's; got %s", got)
	}

	// Shared entries are present (via symlink or copy).
	for _, name := range []string{"settings.json", "CLAUDE.md", "skills"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected shared %s in session: %v", name, err)
		}
	}
	// settings.json content reachable through the link.
	sj, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(sj) != `{"theme":"dark"}` {
		t.Errorf("shared settings.json content = %s", sj)
	}

	// --no-share gives a bare profile (no customizations, still its own cred).
	st2 := &Store{Root: t.TempDir()}
	bare, err := st2.PrepareSession(a, raw, false, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bare, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("bare profile should not have settings.json, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(bare, ".credentials.json")); err != nil {
		t.Errorf("bare profile still needs its credential: %v", err)
	}
}

func TestRemove(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _ := AccountFromBlob(sampleBlob("rm-token", "max"), "gone", time.Now())
	if err := st.Save(a, sampleBlob("rm-token", "max")); err != nil {
		t.Fatal(err)
	}
	// Give it a session profile too, so we can prove that's cleaned up.
	if _, err := st.PrepareSession(a, sampleBlob("rm-token", "max"), false, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := st.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Fatalf("after Remove, list = %v, want empty", list)
	}
	if _, err := os.Stat(st.SessionDir(a.ID)); !os.IsNotExist(err) {
		t.Errorf("session profile should be gone, err=%v", err)
	}
}

func TestPurge(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, tok := range []string{"p1", "p2", "p3"} {
		a, _ := AccountFromBlob(sampleBlob(tok, "max"), tok, time.Now())
		if err := st.Save(a, sampleBlob(tok, "max")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.BackupLive(sampleBlob("p1", "max"), "20260615-000000"); err != nil {
		t.Fatal(err)
	}
	if err := st.Purge(); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Fatalf("after Purge, list = %v, want empty", list)
	}
	for _, d := range []string{st.accountsDir(), st.sessionsDir(), st.backupsDir()} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s should be gone after purge, err=%v", d, err)
		}
	}
}

func TestBackupLive(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	path, err := st.BackupLive(sampleBlob("live", "max"), "20260615-000000")
	if err != nil || path == "" {
		t.Fatalf("BackupLive = %q, %v", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	// Empty blob is a no-op.
	p2, err := st.BackupLive(nil, "x")
	if err != nil || p2 != "" {
		t.Fatalf("BackupLive(nil) = %q, %v; want no-op", p2, err)
	}
}

func TestFingerprintOf(t *testing.T) {
	if FingerprintOf([]byte("garbage")) != "" {
		t.Fatal("garbage should yield empty fingerprint")
	}
	a, _ := parseBlob(sampleBlob("fp", "max"))
	if FingerprintOf(sampleBlob("fp", "max")) != a.ID {
		t.Fatal("FingerprintOf disagrees with parseBlob id")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
