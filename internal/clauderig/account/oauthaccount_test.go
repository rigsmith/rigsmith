package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ~/.claude.json holds far more than the identity block — project state,
// history, per-org caches, ~75 KB in practice. A truncating write that fails
// partway is real data loss, and it would also make the switch rollback's
// "nothing changed" message untrue. These tests pin the surgical + atomic
// contract.

func writeConfig(t *testing.T, dir string, v map[string]any) string {
	t.Helper()
	p := filepath.Join(dir, ".claude.json")
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteOAuthAccountPreservesEverythingElse(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, map[string]any{
		"oauthAccount": map[string]any{"emailAddress": "old@x.com", "organizationUuid": "org-old"},
		"projects":     map[string]any{"/a": map[string]any{"k": 1}, "/b": map[string]any{"k": 2}},
		"someCache":    []any{1, 2, 3},
		"flag":         true,
	})

	newBlock, _ := json.Marshal(map[string]any{"emailAddress": "new@x.com", "organizationUuid": "org-new"})
	if err := writeOAuthAccountTo(p, newBlock); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("config is no longer valid JSON after the write: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected all 4 top-level keys to survive, got %d: %v", len(got), got)
	}
	if projects, ok := got["projects"].(map[string]any); !ok || len(projects) != 2 {
		t.Errorf("projects should be untouched, got %v", got["projects"])
	}
	if got["flag"] != true {
		t.Errorf("unrelated keys should be untouched, got flag=%v", got["flag"])
	}
	oa, ok := got["oauthAccount"].(map[string]any)
	if !ok || oa["emailAddress"] != "new@x.com" || oa["organizationUuid"] != "org-new" {
		t.Errorf("oauthAccount was not replaced, got %v", got["oauthAccount"])
	}
	// The mode of the destination must survive — this file holds account state.
	if fi, serr := os.Stat(p); serr != nil {
		t.Fatal(serr)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("expected mode 0600 to be preserved, got %v", fi.Mode().Perm())
	}
}

// A missing destination is a no-op, not an error — but callers that are about to
// move a credential must check GlobalConfigExists first, or they'd swap the
// credential against a profile that was never written.
func TestWriteOAuthAccountNoOpsOnMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude.json")
	if err := writeOAuthAccountTo(p, []byte(`{"emailAddress":"a@x.com"}`)); err != nil {
		t.Fatalf("a missing config should be a silent no-op, got %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("no-op must not create the file")
	}
}

// The write must be all-or-nothing. A rename-based replace leaves the original
// intact when anything fails, where a truncating write would not.
func TestWriteOAuthAccountLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, map[string]any{
		"oauthAccount": map[string]any{"emailAddress": "old@x.com"},
		"keep":         "me",
	})
	block, _ := json.Marshal(map[string]any{"emailAddress": "new@x.com"})
	if err := writeOAuthAccountTo(p, block); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected only the config to remain, got %d entries", len(entries))
	}
}

// atomicWriteFile is what makes the failure path safe: on error the destination
// must still hold its previous contents, never a fragment.
func TestAtomicWriteFileLeavesOriginalIntactOnFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	original := []byte(`{"important":"data"}`)
	if err := os.WriteFile(p, original, 0o600); err != nil {
		t.Fatal(err)
	}

	// Make the directory unwritable so the temp file can't be created; the
	// destination must be untouched. (Skipped as root, which ignores the bit.)
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := atomicWriteFile(p, []byte("replacement"), 0o600); err == nil {
		t.Fatal("expected an error when the temp file cannot be created")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("destination must be unchanged on failure, got %q", got)
	}
}
