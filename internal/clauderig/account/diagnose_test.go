package account

import (
	"strings"
	"testing"
)

// The invariant these tests encode: for real Claude Code state the credential's
// organizationUuid and ~/.claude.json's oauthAccount.organizationUuid name the
// SAME org. The rest of this package's fixtures generate the two from unrelated
// strings, so nothing here ever asserted it — which is why a live desync (one org
// in the Keychain, another in the profile block, for two weeks) went unnoticed
// until artifacts started landing on the wrong account.

func TestProblemsFlagsOrgDesync(t *testing.T) {
	o := Observation{
		CredOrg:    "org-alpha",
		BlockOrg:   "org-beta",
		BlockEmail: "john@beta.example",
	}
	probs := o.Problems()
	if len(probs) == 0 {
		t.Fatal("expected a desync problem when credential org != block org")
	}
	joined := strings.Join(probs, "\n")
	for _, want := range []string{"DESYNC", "org-alp", "org-bet", "john@beta.example"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problem text missing %q:\n%s", want, joined)
		}
	}
}

func TestProblemsSilentWhenHalvesAgree(t *testing.T) {
	o := Observation{CredOrg: "org-same", BlockOrg: "org-same", BlockEmail: "j@x.com"}
	if probs := o.Problems(); len(probs) != 0 {
		t.Fatalf("expected no problems when both halves agree, got %v", probs)
	}
}

// An absent half is unknown, not a conflict — a machine that isn't logged in must
// not be reported as desynced.
func TestProblemsIgnoresMissingHalf(t *testing.T) {
	for name, o := range map[string]Observation{
		"no block":      {CredOrg: "org-a"},
		"no credential": {BlockOrg: "org-a", BlockEmail: "j@x.com"},
		"neither":       {},
	} {
		if probs := o.Problems(); len(probs) != 0 {
			t.Errorf("%s: expected no problems, got %v", name, probs)
		}
	}
}

func TestProblemsFlagsActivePointerDrift(t *testing.T) {
	o := Observation{
		CredOrg:     "org-live",
		BlockOrg:    "org-live",
		ActiveOrg:   "org-stored",
		ActiveEmail: "other@x.com",
	}
	probs := o.Problems()
	if len(probs) != 1 || !strings.Contains(probs[0], "active account") {
		t.Fatalf("expected an active-pointer problem, got %v", probs)
	}
}

func TestRecordOnlyAppendsOnIdentityChange(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	base := Observation{At: "t1", CredOrg: "org-a", BlockOrg: "org-a", BlockEmail: "j@x.com"}

	if wrote, err := st.Record(base); err != nil || !wrote {
		t.Fatalf("first observation should be recorded: wrote=%v err=%v", wrote, err)
	}

	// Same identity, different churny fields — must NOT append, or a poll loop
	// would grow the journal without bound and bury the real events.
	noise := base
	noise.At = "t2"
	noise.ConfigModified = "later"
	noise.ConfigSize = 999
	noise.Live = []Instance{{PID: 42, Kind: "cli"}}
	if wrote, err := st.Record(noise); err != nil || wrote {
		t.Fatalf("unchanged identity should not append: wrote=%v err=%v", wrote, err)
	}

	// A real flip appends and explains itself.
	flip := base
	flip.At = "t3"
	flip.BlockOrg = "org-b"
	flip.BlockEmail = "other@x.com"
	if wrote, err := st.Record(flip); err != nil || !wrote {
		t.Fatalf("identity change should append: wrote=%v err=%v", wrote, err)
	}

	entries, err := st.Journal(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 journal entries, got %d", len(entries))
	}
	last := entries[1]
	if last.PreviousAt != "t1" {
		t.Errorf("expected PreviousAt to link to the prior entry, got %q", last.PreviousAt)
	}
	changed := strings.Join(last.Changed, "; ")
	if !strings.Contains(changed, "blockOrg") || !strings.Contains(changed, "org-b") {
		t.Errorf("Changed should name the flipped field and its new value, got %q", changed)
	}
}

func TestJournalLimitReturnsNewest(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, org := range []string{"org-1", "org-2", "org-3"} {
		if _, err := st.Record(Observation{At: org, CredOrg: org}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := st.Journal(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].CredOrg != "org-2" || entries[1].CredOrg != "org-3" {
		t.Fatalf("expected the two newest entries, got %+v", entries)
	}
}

func TestJournalMissingFileIsEmpty(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	entries, err := st.Journal(0)
	if err != nil {
		t.Fatalf("a missing journal is not an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}
