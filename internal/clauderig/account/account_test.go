package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOAuthAccountReadWritePreservesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude.json")
	mustWrite(t, p, `{
  "numStartups": 5,
  "oauthAccount": {"emailAddress":"old@x.com","seatTier":"pro"},
  "projects": {"keep":true}
}`)
	raw, err := readOAuthAccountFrom(p)
	if err != nil || parseOAuthMeta(raw).EmailAddress != "old@x.com" {
		t.Fatalf("read: %s, %v", raw, err)
	}
	if err := writeOAuthAccountTo(p, []byte(`{"emailAddress":"new@y.com","seatTier":"max"}`)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	for _, keep := range []string{`"numStartups"`, `"keep"`} {
		if !strings.Contains(string(after), keep) {
			t.Errorf("write dropped %s:\n%s", keep, after)
		}
	}
	raw2, _ := readOAuthAccountFrom(p)
	if m := parseOAuthMeta(raw2); m.EmailAddress != "new@y.com" || m.SeatTier != "max" {
		t.Errorf("oauthAccount not replaced: %+v", m)
	}
	// Missing file → (nil, nil), and writing to it is a harmless no-op.
	missing := filepath.Join(t.TempDir(), "nope.json")
	if r, err := readOAuthAccountFrom(missing); err != nil || r != nil {
		t.Errorf("missing read = %v, %v", r, err)
	}
	if err := writeOAuthAccountTo(missing, []byte(`{}`)); err != nil {
		t.Errorf("missing write should be no-op, got %v", err)
	}
}

// sampleBlob builds a credential blob with a given access token and subscription.
func sampleBlob(tok, sub string) []byte {
	m := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "acc-" + tok,
			"refreshToken":     "ref-" + tok,
			"subscriptionType": sub,
		},
		"organizationUuid": "org-" + tok,
	}
	b, _ := json.Marshal(m)
	return b
}

// sampleOAuth builds an oauthAccount block; the org is derived from the email so
// re-capturing the same email maps to the same account.
func sampleOAuth(email string) []byte { return sampleOAuthOrg(email, "org:"+email) }

func sampleOAuthOrg(email, org string) []byte {
	b, _ := json.Marshal(map[string]any{"emailAddress": email, "organizationUuid": org, "seatTier": "max"})
	return b
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"work":          "work",
		"Work Laptop":   "work-laptop",
		"john@acme.com": "john-acme-com",
		"  spaced  ":    "spaced",
		"!!!":           "",
		"a--b":          "a-b",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaFromBlob(t *testing.T) {
	sub, org, err := metaFromBlob(sampleBlob("t", "max"))
	if err != nil || sub != "max" || org != "org-t" {
		t.Fatalf("metaFromBlob = %q,%q,%v", sub, org, err)
	}
	if _, _, err := metaFromBlob([]byte(`{"claudeAiOauth":{}}`)); err == nil {
		t.Error("tokenless blob should error")
	}
	if _, _, err := metaFromBlob([]byte("nope")); err == nil {
		t.Error("invalid json should error")
	}
}

func TestCaptureLiveResolveAndUpdate(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, existed, err := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("work@acme.com"))
	if err != nil || existed {
		t.Fatalf("first capture: existed=%v err=%v", existed, err)
	}
	if a.ID != "work-acme-com" || a.Email != "work@acme.com" || a.SubscriptionType != "max" {
		t.Fatalf("account = %+v", a)
	}
	// Re-capture same email → update, not duplicate.
	_, existed, err = st.CaptureLive(sampleBlob("w2", "max"), sampleOAuth("work@acme.com"))
	if err != nil || !existed {
		t.Fatalf("re-capture: existed=%v err=%v", existed, err)
	}
	if list, _ := st.List(); len(list) != 1 {
		t.Fatalf("expected 1 account after re-capture, got %d", len(list))
	}
	// Resolve by id, full email, and email/id prefix.
	for _, ref := range []string{"work-acme-com", "work@acme.com", "work@"} {
		if got, err := st.Resolve(ref); err != nil || got.ID != "work-acme-com" {
			t.Errorf("Resolve(%q) = %+v, %v", ref, got, err)
		}
	}
	fi, _ := os.Stat(st.credPath(a.ID))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credential perms = %v, want 0600", fi.Mode().Perm())
	}
}

func TestCaptureLiveSameEmailDifferentOrg(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a1, existed, _ := st.CaptureLive(sampleBlob("x", "pro"), sampleOAuthOrg("john@x.com", "orgA"))
	if existed || a1.ID != "john-x-com" {
		t.Fatalf("first = %+v existed=%v", a1, existed)
	}
	// Same email, DIFFERENT org → a distinct account with a numeric suffix.
	a2, existed, _ := st.CaptureLive(sampleBlob("y", "max"), sampleOAuthOrg("john@x.com", "orgB"))
	if existed || a2.ID != "john-x-com-2" {
		t.Fatalf("second = %+v existed=%v, want new id john-x-com-2", a2, existed)
	}
	if list, _ := st.List(); len(list) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(list))
	}
	// Re-capturing orgA updates the first, no third account.
	a3, existed, _ := st.CaptureLive(sampleBlob("z", "pro"), sampleOAuthOrg("john@x.com", "orgA"))
	if !existed || a3.ID != "john-x-com" {
		t.Fatalf("re-capture orgA = %+v existed=%v", a3, existed)
	}
}

func TestResolveFuzzy(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	st.CaptureLive(sampleBlob("r", "max"), sampleOAuth("john@relatecpa.com"))
	st.CaptureLive(sampleBlob("b", "pro"), sampleOAuth("john@brightshore.io"))

	for ref, wantEmail := range map[string]string{
		"relate":              "john@relatecpa.com",
		"rel":                 "john@relatecpa.com",
		"bright":              "john@brightshore.io",
		"bri":                 "john@brightshore.io",
		"BRIGHT":              "john@brightshore.io", // case-insensitive
		"john@relatecpa.com":  "john@relatecpa.com",  // exact email
		"john-brightshore-io": "john@brightshore.io", // exact id
	} {
		got, err := st.Resolve(ref)
		if err != nil || got.Email != wantEmail {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got.Email, err, wantEmail)
		}
	}
	// A shared substring is ambiguous and lists the candidates.
	if _, err := st.Resolve("john"); err == nil {
		t.Error("'john' should be ambiguous across both accounts")
	}
	if _, err := st.Resolve("nope"); err == nil {
		t.Error("'nope' should match nothing")
	}
}

func TestCaptureLiveRequiresEmail(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	// No oauthAccount → no email → can't key the account.
	if _, _, err := st.CaptureLive(sampleBlob("a", "max"), nil); err == nil {
		t.Error("capture without an email should error")
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Error("a failed capture should not create an account")
	}
}

func TestActivePointer(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if id, _ := st.Active(); id != "" {
		t.Fatalf("fresh store active = %q, want empty", id)
	}
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	if err := st.SetActive(a.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := st.Active(); id != a.ID {
		t.Fatalf("active = %q, want %q", id, a.ID)
	}
	// Removing the active account clears the pointer.
	if err := st.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if id, _ := st.Active(); id != "" {
		t.Errorf("after removing active, pointer = %q, want empty", id)
	}
}

func TestEnsureSessionSeedShareAndStale(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, "settings.json"), `{"theme":"dark"}`)
	mustWrite(t, filepath.Join(home, "skills", "x.md"), "skill")

	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	dir, err := st.EnsureSession(a, true, home)
	if err != nil {
		t.Fatal(err)
	}
	// Seeded its own credential + shared customizations (not credentials/history).
	if !fileExists(filepath.Join(dir, ".credentials.json")) {
		t.Error("session credential not seeded")
	}
	for _, n := range []string{"settings.json", "skills"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("shared %s missing: %v", n, err)
		}
	}

	// Simulate the session self-refreshing its token, then a NON-stale re-run:
	// the refreshed credential must be preserved, not clobbered from the store.
	refreshed := []byte(`{"claudeAiOauth":{"accessToken":"REFRESHED"}}`)
	mustWrite(t, filepath.Join(dir, ".credentials.json"), string(refreshed))
	if _, err := st.EnsureSession(a, false, home); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(got) != string(refreshed) {
		t.Error("EnsureSession clobbered a self-refreshed session credential")
	}

	// Updating the stored credential marks the profile stale → next run re-seeds.
	if _, _, err := st.CaptureLive(sampleBlob("w3", "max"), sampleOAuth("w@x.com")); err != nil {
		t.Fatal(err)
	}
	if !fileExists(st.stalePath(a.ID)) {
		t.Fatal("re-capture should mark the existing session stale")
	}
	if _, err := st.EnsureSession(a, false, home); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if string(got) == string(refreshed) {
		t.Error("stale session should have been re-seeded from the updated store credential")
	}
	if fileExists(st.stalePath(a.ID)) {
		t.Error("stale marker should be cleared after re-seed")
	}
}

func TestRemovePurge(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), sampleOAuth("w@x.com"))
	st.EnsureSession(a, false, t.TempDir())
	if err := st.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Fatalf("after Remove, %d accounts left", len(list))
	}

	st.CaptureLive(sampleBlob("a", "max"), sampleOAuth("a@x.com"))
	st.CaptureLive(sampleBlob("b", "max"), sampleOAuth("b@x.com"))
	st.BackupLive(sampleBlob("a", "max"), "20260615-000000")
	if err := st.Purge(); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Fatalf("after Purge, %d accounts left", len(list))
	}
	if dirExists(st.backupsDir()) {
		t.Error("purge should remove cred-backups")
	}
}

func TestBackupLive(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	path, err := st.BackupLive(sampleBlob("x", "max"), "20260615-000000")
	if err != nil || path == "" || !fileExists(path) {
		t.Fatalf("BackupLive = %q, %v", path, err)
	}
	if p, err := st.BackupLive(nil, "x"); err != nil || p != "" {
		t.Fatalf("BackupLive(nil) = %q, %v; want no-op", p, err)
	}
}

// The symlink-fallback copy must preserve EVERY file, including names a filtered
// copier would skip — e.g. a plugin's bundled node_modules.
func TestCopyAllPreservesEverything(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "plugins", "p", "node_modules", "dep", "index.js"), "x")
	mustWrite(t, filepath.Join(src, "plugins", "p", "manifest.json"), "{}")
	dst := filepath.Join(t.TempDir(), "out")
	if err := copyAll(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"plugins/p/node_modules/dep/index.js", "plugins/p/manifest.json"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("copyAll dropped %s: %v", rel, err)
		}
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
