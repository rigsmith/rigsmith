package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
	a, existed, err := st.CaptureLive(sampleBlob("w", "max"), "Work Laptop")
	if err != nil || existed {
		t.Fatalf("first capture: existed=%v err=%v", existed, err)
	}
	if a.ID != "work-laptop" || a.Label != "Work Laptop" || a.SubscriptionType != "max" {
		t.Fatalf("account = %+v", a)
	}
	// Re-capture same label → update, not duplicate.
	_, existed, err = st.CaptureLive(sampleBlob("w2", "max"), "Work Laptop")
	if err != nil || !existed {
		t.Fatalf("re-capture: existed=%v err=%v", existed, err)
	}
	if list, _ := st.List(); len(list) != 1 {
		t.Fatalf("expected 1 account after re-capture, got %d", len(list))
	}
	// Resolve by id, label, and slug-of-label.
	for _, ref := range []string{"work-laptop", "Work Laptop", "work-lap"} {
		if got, err := st.Resolve(ref); err != nil || got.ID != "work-laptop" {
			t.Errorf("Resolve(%q) = %+v, %v", ref, got, err)
		}
	}
	// Credential perms 0600.
	fi, _ := os.Stat(st.credPath(a.ID))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credential perms = %v, want 0600", fi.Mode().Perm())
	}
}

func TestCaptureLiveAutoLabel(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	a1, _, _ := st.CaptureLive(sampleBlob("a", "max"), "")
	a2, _, _ := st.CaptureLive(sampleBlob("b", "max"), "")
	if a1.ID != "account-1" || a2.ID != "account-2" {
		t.Fatalf("auto ids = %q, %q, want account-1/account-2", a1.ID, a2.ID)
	}
}

func TestActivePointer(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if id, _ := st.Active(); id != "" {
		t.Fatalf("fresh store active = %q, want empty", id)
	}
	st.CaptureLive(sampleBlob("w", "max"), "work")
	if err := st.SetActive("work"); err != nil {
		t.Fatal(err)
	}
	if id, _ := st.Active(); id != "work" {
		t.Fatalf("active = %q, want work", id)
	}
	// Removing the active account clears the pointer.
	if err := st.Remove("work"); err != nil {
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

	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), "work")
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
	if _, _, err := st.CaptureLive(sampleBlob("w3", "max"), "work"); err != nil {
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
	a, _, _ := st.CaptureLive(sampleBlob("w", "max"), "work")
	st.EnsureSession(a, false, t.TempDir())
	if err := st.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.List(); len(list) != 0 {
		t.Fatalf("after Remove, %d accounts left", len(list))
	}

	st.CaptureLive(sampleBlob("a", "max"), "a")
	st.CaptureLive(sampleBlob("b", "max"), "b")
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
