package account

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// seedAccount writes a minimal tracked account straight into the store, so these
// tests exercise alias/disable without a live credential.
func seedAccount(t *testing.T, s *Store, id, email string) {
	t.Helper()
	if err := os.MkdirAll(s.acctDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(Account{ID: id, Email: email})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.metaPath(id), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func ergoStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	seedAccount(t, s, "john-work-com", "john@work.com")
	seedAccount(t, s, "john-personal-com", "john@personal.com")
	return s
}

func TestSetAliasThenResolveByIt(t *testing.T) {
	s := ergoStore(t)
	if err := s.SetAlias("john-work-com", "dev"); err != nil {
		t.Fatal(err)
	}
	a, err := s.Resolve("dev")
	if err != nil {
		t.Fatalf("Resolve(dev): %v", err)
	}
	if a.ID != "john-work-com" {
		t.Fatalf("resolved %q", a.ID)
	}
	// Aliases are typed by hand; case should not decide whether a switch lands.
	if a, err := s.Resolve("DEV"); err != nil || a.ID != "john-work-com" {
		t.Fatalf("Resolve(DEV) = %v, %v — alias matching must be case-insensitive", a.ID, err)
	}
}

// An alias that shadows another account's identity would silently redirect a
// switch to the wrong login — the exact failure this whole package exists to
// prevent.
func TestSetAliasRefusesToShadowAnotherAccount(t *testing.T) {
	s := ergoStore(t)
	if err := s.SetAlias("john-personal-com", "keeper"); err != nil {
		t.Fatal(err)
	}
	// Attempted from the OTHER account: an id, an email and an alias that all
	// already identify john@personal.com.
	for _, shadow := range []string{"john-personal-com", "john@personal.com", "keeper", "KEEPER"} {
		if err := s.SetAlias("john-work-com", shadow); err == nil {
			t.Errorf("SetAlias(%q) was allowed — it shadows another account", shadow)
		}
	}
	// Re-setting an account's own alias is not shadowing.
	if err := s.SetAlias("john-personal-com", "keeper"); err != nil {
		t.Fatalf("re-setting an account's own alias was refused: %v", err)
	}
}

func TestSetAliasValidatesTheHandle(t *testing.T) {
	s := ergoStore(t)
	for _, bad := range []string{"", " ", "has space", "-lead", ".dot", strings.Repeat("x", 33), "a/b"} {
		if err := s.SetAlias("john-work-com", bad); err == nil {
			t.Errorf("SetAlias(%q) = nil, want an error", bad)
		}
	}
	for _, good := range []string{"dev", "work-2", "a.b", "x_y", "A1"} {
		if err := s.SetAlias("john-work-com", good); err != nil {
			t.Errorf("SetAlias(%q) = %v, want nil", good, err)
		}
	}
}

func TestClearAlias(t *testing.T) {
	s := ergoStore(t)
	if err := s.SetAlias("john-work-com", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearAlias("john-work-com"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.read("john-work-com")
	if a.Alias != "" {
		t.Fatalf("alias = %q, want empty", a.Alias)
	}
	if _, err := s.Resolve("dev"); err == nil {
		t.Fatal("a cleared alias still resolves")
	}
}

// The identity fields are the ones a bad write would destroy, so a label edit
// must leave them exactly as they were.
func TestUpdateMetaPreservesIdentityFields(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	full := Account{
		ID: "john-work-com", Email: "john@work.com",
		SubscriptionType: "max", OrganizationUUID: "org-uuid", AddedAt: "2026-01-01T00:00:00Z",
	}
	if err := os.MkdirAll(s.acctDir(full.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(full)
	if err := os.WriteFile(s.metaPath(full.ID), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias(full.ID, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(full.ID, true); err != nil {
		t.Fatal(err)
	}
	got, ok := s.read(full.ID)
	if !ok {
		t.Fatal("account unreadable after a label edit")
	}
	if got.Email != full.Email || got.SubscriptionType != full.SubscriptionType ||
		got.OrganizationUUID != full.OrganizationUUID || got.AddedAt != full.AddedAt {
		t.Fatalf("identity fields changed: %+v", got)
	}
	if got.Alias != "dev" || !got.Disabled {
		t.Fatalf("edits not applied: %+v", got)
	}
}

// A label edit must never rewrite the credential — that read-modify-write is the
// operation that has already cost this project two logouts.
func TestUpdateMetaDoesNotTouchTheCredential(t *testing.T) {
	s := ergoStore(t)
	cred := []byte(`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref"},"organizationUuid":"org"}`)
	if err := os.WriteFile(s.credPath("john-work-com"), cred, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(s.credPath("john-work-com"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("john-work-com", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled("john-work-com", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(s.credPath("john-work-com"))
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the credential was rewritten by a label edit")
	}
	got, rerr := os.ReadFile(s.credPath("john-work-com"))
	if rerr != nil || string(got) != string(cred) {
		t.Fatalf("credential contents changed: %s", got)
	}
}

func TestEnabledExcludesDisabledAccounts(t *testing.T) {
	s := ergoStore(t)
	if err := s.SetDisabled("john-work-com", true); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.Enabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].ID != "john-personal-com" {
		t.Fatalf("Enabled() = %+v, want only the personal account", enabled)
	}
	// Disabled is about AUTOMATIC selection only: naming it must still work.
	if _, rerr := s.Resolve("john-work-com"); rerr != nil {
		t.Fatalf("a disabled account stopped resolving by name: %v", rerr)
	}
}
