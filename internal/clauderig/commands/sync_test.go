package commands

import (
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"os"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// machineName identifies the local machine by its stable path identity (OS token
// + home directory), not by picking an arbitrary map entry. With more than one
// registered machine it must resolve deterministically to the matching one,
// regardless of Go's randomized map iteration order.
func TestMachineName_DeterministicLocalMatch(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Machines: map[string]config.Machine{
			"other": {Name: "other", OS: "definitely-not-this-os", Home: "/nope/elsewhere"},
			"local": {Name: "local", OS: config.OSToken(), Home: home},
		},
	}

	// Loop to defeat map-iteration randomness: the result must be stable.
	for i := 0; i < 20; i++ {
		if got := machineName(cfg); got != "local" {
			t.Fatalf("iteration %d: machineName = %q, want %q", i, got, "local")
		}
	}
}

// With no registered machines, machineName falls back to a non-empty string
// (the OS hostname, or "this").
func TestMachineName_EmptyMachinesFallback(t *testing.T) {
	cfg := &config.Config{Machines: map[string]config.Machine{}}
	if got := machineName(cfg); got == "" {
		t.Fatalf("machineName returned empty string for empty Machines")
	}
}

// The device registry is written after engine.Sync's scan and committed
// directly, so nothing else ever looks at these three values. The argument that
// a uuid and an email cannot trip the tripwire is about their SHAPE — so when
// the shape is wrong, this is the only thing standing between a token-like
// identity and the remote.
func TestScanIdentity(t *testing.T) {
	ok := &devices.Account{
		AccountUUID:      "456fc32e-7579-49c7-bb2a-099657892c6a",
		OrganizationUUID: "f1eab509-9590-47cf-a4e8-33e5f45a5747",
		Email:            "john@example.com",
	}
	if f := scanIdentity(ok); f != nil {
		t.Errorf("ordinary identity flagged as %s (%s)", f.Path, f.Kind)
	}
	if f := scanIdentity(&devices.Account{}); f != nil {
		t.Errorf("empty identity flagged: %+v", f)
	}

	// A rewritten oauthAccount carrying something token-shaped must not travel.
	bad := &devices.Account{
		AccountUUID: "ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		Email:       "john@example.com",
	}
	f := scanIdentity(bad)
	if f == nil {
		t.Fatal("a token-shaped accountUuid must be caught")
	}
	if f.Path != "accountUuid" {
		t.Errorf("finding names %q, want the offending field", f.Path)
	}
}

// ScanFile skips its entropy check for multiline values — reasonable for a
// file, wrong for an identity field, where a newline would carry whatever
// follows it past the scan and into the pushed registry.
func TestScanIdentity_RejectsMultilineValues(t *testing.T) {
	f := scanIdentity(&devices.Account{
		AccountUUID: "456fc32e-7579-49c7-bb2a-099657892c6a\nghp_0123456789abcdefghijklmnop",
		Email:       "john@example.com",
	})
	if f == nil {
		t.Fatal("a multiline identity value must be rejected")
	}
	if f.Path != "accountUuid" {
		t.Errorf("finding names %q, want the offending field", f.Path)
	}
}

// ScanFile returns NOTHING above its content cap, so an oversized value is
// unscanned rather than clean — and an identity that begins with a token would
// have been committed and pushed on that silence.
func TestScanIdentity_RejectsOversizedValues(t *testing.T) {
	huge := "ghp_" + strings.Repeat("a", maxIdentityBytes)
	f := scanIdentity(&devices.Account{AccountUUID: huge, Email: "john@example.com"})
	if f == nil {
		t.Fatal("an oversized identity value must be rejected")
	}
	if f.Path != "accountUuid" {
		t.Errorf("finding names %q, want the offending field", f.Path)
	}
	// a real uuid is nowhere near the cap
	if f := scanIdentity(&devices.Account{
		AccountUUID: "456fc32e-7579-49c7-bb2a-099657892c6a", Email: "john@example.com"}); f != nil {
		t.Errorf("an ordinary identity was rejected: %+v", f)
	}
}
