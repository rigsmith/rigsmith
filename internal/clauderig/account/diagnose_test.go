package account

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"sync"
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

// A machine with no credential (or nothing at all) can't be compared, so it must
// not be reported as desynced.
func TestProblemsIgnoresUncomparableState(t *testing.T) {
	for name, o := range map[string]Observation{
		"no credential": {BlockOrg: "org-a", BlockEmail: "j@x.com"},
		"neither":       {},
	} {
		if probs := o.Problems(); len(probs) != 0 {
			t.Errorf("%s: expected no problems, got %v", name, probs)
		}
	}
}

// A credential with no profile organization is NOT a clean bill of health:
// Claude Code has no identity to display and `add` can't key an account from it,
// so reporting "both halves agree" would be exactly the false reassurance this
// command exists to remove.
func TestProblemsFlagsCredentialWithNoProfileOrg(t *testing.T) {
	o := Observation{CredOrg: "org-a"}
	probs := o.Problems()
	if len(probs) != 1 {
		t.Fatalf("expected one problem for a credential with no profile org, got %v", probs)
	}
	if !strings.Contains(probs[0], "no oauthAccount organization") {
		t.Errorf("problem should name the missing profile org, got %q", probs[0])
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

// Running `doctor` while `watch` polls is ordinary. Unguarded, both read the
// same previous entry and both append the same transition — duplicating events
// and cross-linking PreviousAt to the wrong line.
func TestRecordIsSerializedAcrossConcurrentWriters(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	o := Observation{At: "t1", CredOrg: "org-a", BlockOrg: "org-a", BlockEmail: "j@x.com"}

	const writers = 8
	var wg sync.WaitGroup
	wrote := make([]bool, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wrote[i], errs[i] = st.Record(o)
		}(i)
	}
	wg.Wait()

	recorded := 0
	for i := range wrote {
		if errs[i] != nil {
			t.Fatalf("writer %d errored: %v", i, errs[i])
		}
		if wrote[i] {
			recorded++
		}
	}
	if recorded != 1 {
		t.Fatalf("exactly one writer should record an identical observation, got %d", recorded)
	}
	entries, err := st.Journal(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 journal entry, got %d", len(entries))
	}
	if _, serr := os.Stat(st.journalPath() + ".lock"); !os.IsNotExist(serr) {
		t.Error("lock file should be released after Record returns")
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

// The repair writes one account's profile block over another's, so picking the
// wrong account here would deepen the exact bug it repairs.
func TestMatchByOrg(t *testing.T) {
	all := []Account{
		{ID: "a", Email: "a@x.com", OrganizationUUID: "org-a"},
		{ID: "b", Email: "b@x.com", OrganizationUUID: "org-b"},
		{ID: "legacy", Email: "legacy@x.com"}, // captured before org was recorded
	}
	if got, _ := matchByOrg(all, "org-b"); got == nil || got.ID != "b" {
		t.Fatalf("expected account b, got %+v", got)
	}
	if got, _ := matchByOrg(all, "org-missing"); got != nil {
		t.Fatalf("expected no match for an unknown org, got %+v", got)
	}
	// An empty org must not wildcard onto the account with no org recorded.
	if got, _ := matchByOrg(all, ""); got != nil {
		t.Fatalf("empty org must never match, got %+v", got)
	}
	if got, _ := matchByOrg(nil, "org-a"); got != nil {
		t.Fatalf("expected no match in an empty store, got %+v", got)
	}
}

// Two logins can share an org, and a credential names only the org — so there is
// nothing to disambiguate with. Returning the first would stamp one identity's
// profile block over another's.
func TestMatchByOrgRefusesWhenSeveralAccountsShareTheOrg(t *testing.T) {
	all := []Account{
		{ID: "one", Email: "one@shared.com", OrganizationUUID: "org-shared"},
		{ID: "two", Email: "two@shared.com", OrganizationUUID: "org-shared"},
		{ID: "solo", Email: "solo@x.com", OrganizationUUID: "org-solo"},
	}
	match, candidates := matchByOrg(all, "org-shared")
	if match != nil {
		t.Fatalf("an ambiguous org must not resolve to a single account, got %+v", match)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected both candidates returned so the caller can explain, got %d", len(candidates))
	}
	// The unambiguous case still resolves.
	if m, c := matchByOrg(all, "org-solo"); m == nil || m.ID != "solo" || len(c) != 1 {
		t.Fatalf("a single-account org should still match, got %+v / %d candidates", m, len(c))
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

// TestLockNameBusyOnlyClaimsTheWindowsCase pins the predicate that decides
// whether an exclusive-create failure is worth retrying.
//
// Windows answers ERROR_ACCESS_DENIED — which Go maps to fs.ErrPermission —
// while a lock file is pending deletion, so a contended lock reported "Access is
// denied" instead of waiting its turn. Unix never does this (it says EEXIST,
// which os.IsExist already handles), so the predicate must stay false there or a
// genuine permission fault would spin for the full deadline.
func TestLockNameBusyOnlyClaimsTheWindowsCase(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "journal.lock", Err: fs.ErrPermission}
	if got, want := lockNameBusy(denied), runtime.GOOS == "windows"; got != want {
		t.Errorf("lockNameBusy(permission denied) = %v on %s, want %v", got, runtime.GOOS, want)
	}
	if lockNameBusy(&fs.PathError{Op: "open", Path: "journal.lock", Err: fs.ErrExist}) {
		t.Error("ErrExist is the os.IsExist path — this predicate must not also claim it")
	}
	if lockNameBusy(errors.New("disk on fire")) {
		t.Error("an unrelated error must stay fatal")
	}
}
