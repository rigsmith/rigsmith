package account

import (
	"os"
	"path/filepath"
	"testing"
)

// LiveIdentity must return the three identity fields and nothing else — the
// same block holds the plan, the rate-limit tier and the org name, none of
// which are safe to write into a synced repo.
func TestIdentityFromFile_ReadsOnlyIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(p, []byte(`{
	  "userID": "deadbeef",
	  "oauthAccount": {
	    "accountUuid": "456fc32e-7579-49c7-bb2a-099657892c6a",
	    "organizationUuid": "f1eab509-9590-47cf-a4e8-33e5f45a5747",
	    "emailAddress": "john@example.com",
	    "seatTier": "max",
	    "organizationName": "Example Org",
	    "billingType": "stripe_subscription"
	  },
	  "mcpServers": {"ctx": {"headers": {"API_KEY": "super-secret-value"}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	a, o, e, err := identityFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if a != "456fc32e-7579-49c7-bb2a-099657892c6a" {
		t.Errorf("accountUUID = %q", a)
	}
	if o != "f1eab509-9590-47cf-a4e8-33e5f45a5747" {
		t.Errorf("orgUUID = %q", o)
	}
	if e != "john@example.com" {
		t.Errorf("email = %q", e)
	}
}

// A machine that has never logged in has no identity — that is a normal state,
// not an error, and must not fail a sync.
func TestIdentityFromFile_AbsentFileIsNotAnError(t *testing.T) {
	a, o, e, err := identityFromFile(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("absent file should not error: %v", err)
	}
	if a != "" || o != "" || e != "" {
		t.Errorf("want all empty, got %q %q %q", a, o, e)
	}
}

// A malformed oauthAccount previously yielded whatever fields happened to
// decode before the error, and the caller — which writes those values into a
// synced registry — could not tell that from a real identity.
func TestIdentityFromFile_MalformedBlockIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(p, []byte(`{"oauthAccount":{"emailAddress":"a@b.com","accountUuid":`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, o, e, err := identityFromFile(p)
	if err == nil {
		t.Fatal("a malformed block must be an error, not a partial identity")
	}
	if a != "" || o != "" || e != "" {
		t.Errorf("partial identity leaked: %q %q %q", a, o, e)
	}
}
