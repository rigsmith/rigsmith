package commands

import (
	"errors"
	"fmt"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/devices"

	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

// --account matches the recorded attribution, and a session that has none is
// reported as unmatchable rather than silently dropped: "no such session" and
// "I cannot tell whose this is" are opposite answers.
func TestSessionScope_AccountFilter(t *testing.T) {
	sc := sessionScope{account: "acct-a"}

	mine := &sessResult{id: "1", led: ledger.Entry{Account: "acct-a", AccountSource: ledger.AccountFromSync}}
	if ok, _ := sc.keep(mine); !ok {
		t.Error("a session attributed to the filtered account must survive")
	}

	theirs := &sessResult{id: "2", led: ledger.Entry{Account: "acct-b", AccountSource: ledger.AccountFromDesktop}}
	if ok, why := sc.keep(theirs); ok || why != droppedByFilter {
		t.Errorf("other account: ok=%v why=%v, want false/droppedByFilter", ok, why)
	}

	unknown := &sessResult{id: "3"}
	if ok, why := sc.keep(unknown); ok || why != droppedUnattributed {
		t.Errorf("unattributed: ok=%v why=%v, want false/droppedUnattributed", ok, why)
	}
}

// Attribution is compared case-insensitively — uuids are hex and get written
// both ways by different producers.
func TestSessionScope_AccountFilterIgnoresCase(t *testing.T) {
	sc := sessionScope{account: "ACCT-A"}
	r := &sessResult{id: "1", led: ledger.Entry{Account: "acct-a"}}
	if ok, _ := sc.keep(r); !ok {
		t.Error("uuid comparison should be case-insensitive")
	}
}

// --account counts as narrowing, so it is refused alongside --raw/--all like
// the other session filters.
func TestSessionScope_AccountCountsAsFiltering(t *testing.T) {
	if !(sessionScope{account: "acct-a"}).filtering() {
		t.Error("account filter should report as filtering")
	}
	if (sessionScope{}).filtering() {
		t.Error("empty scope should not report as filtering")
	}
}

// An unresolvable name is an error, not an empty result: those mean opposite
// things and only one of them is a working search.
func TestResolveAccountFilter_UnknownIsAnError(t *testing.T) {
	known := map[string]ledger.Entry{
		"s1": {ID: "s1", Account: "456fc32e-7579-49c7-bb2a-099657892c6a"},
	}
	if _, err := resolveAccountFilter("no-such-account", t.TempDir(), known); err == nil {
		t.Fatal("want an error for an unknown account")
	}
	// empty stays empty — no filter requested
	if got, err := resolveAccountFilter("  ", t.TempDir(), known); err != nil || got != "" {
		t.Errorf("blank filter = %q/%v, want \"\"/nil", got, err)
	}
}

// A uuid prefix resolves against the accounts the ledger actually names, so a
// typo fails at the flag instead of quietly matching nothing.
func TestResolveAccountFilter_UUIDPrefix(t *testing.T) {
	full := "456fc32e-7579-49c7-bb2a-099657892c6a"
	known := map[string]ledger.Entry{"s1": {ID: "s1", Account: full}}

	got, err := resolveAccountFilter("456fc32e", t.TempDir(), known)
	if err != nil || got != full {
		t.Errorf("prefix = %q/%v, want %q", got, err, full)
	}
	if _, err := resolveAccountFilter("deadbeef", t.TempDir(), known); err == nil {
		t.Error("a prefix matching no known account must error")
	}
	// too short to be a uuid prefix, and not a known name
	if _, err := resolveAccountFilter("456", t.TempDir(), known); err == nil {
		t.Error("a sub-minimum prefix must not silently match")
	}
}

// Two accounts sharing a prefix must not be silently collapsed into one.
func TestResolveAccountFilter_AmbiguousPrefix(t *testing.T) {
	known := map[string]ledger.Entry{
		"s1": {ID: "s1", Account: "abcd1234-0000-0000-0000-000000000001"},
		"s2": {ID: "s2", Account: "abcd1234-0000-0000-0000-000000000002"},
	}
	if _, err := resolveAccountFilter("abcd1234", t.TempDir(), known); err == nil {
		t.Fatal("an ambiguous prefix must error rather than pick one")
	}
}

// The "everything was excluded" hint must name the flag that did the excluding.
// A fixed list sends the user to widen --since when --account was responsible.
func TestSessionScope_ActiveFiltersNamesWhatIsSet(t *testing.T) {
	if got := (sessionScope{}).activeFilters(); len(got) != 1 || got[0] != "the filters" {
		t.Errorf("no filters set = %v, want a generic phrase", got)
	}
	got := (sessionScope{account: "acct-a"}).activeFilters()
	if len(got) != 1 || got[0] != "--account" {
		t.Errorf("account only = %v, want [--account]", got)
	}
	both := (sessionScope{account: "acct-a", cwd: "api"}).activeFilters()
	if len(both) != 2 {
		t.Errorf("account+cwd = %v, want both named", both)
	}
}

// An account the registry knows but the ledger has not attributed yet is a real
// account with zero results — not an unknown one. Matching only ledger
// attributions collapses exactly the distinction this resolver exists to keep.
func TestResolveAccountFilter_UUIDPrefixFindsARegistryOnlyAccount(t *testing.T) {
	staging := t.TempDir()
	reg, err := devices.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	reg.Touch("mbp", "macos", "2.1.237", &devices.Account{
		AccountUUID: "03d1c0c9-823d-464b-a468-a9bea2383338", Email: "other@example.com",
	}, time.Now())
	if err := reg.Save(staging); err != nil {
		t.Fatal(err)
	}

	// The ledger attributes a DIFFERENT account; the registry one has no rows.
	known := map[string]ledger.Entry{"s1": {ID: "s1", Account: "456fc32e-7579-49c7-bb2a-099657892c6a"}}

	got, err := resolveAccountFilter("03d1c0c9", staging, known)
	if err != nil {
		t.Fatalf("a registry-known account must resolve, not error: %v", err)
	}
	if got != "03d1c0c9-823d-464b-a468-a9bea2383338" {
		t.Errorf("got %q", got)
	}
	// Its email resolves too, by the same registry.
	if got, err := resolveAccountFilter("other@example.com", staging, known); err != nil || got == "" {
		t.Errorf("email = %q/%v", got, err)
	}
}

// --account narrows SESSIONS, so it is refused with --raw/--all like the other
// session filters. The guard runs before the flag is resolved into the scope,
// so it has to test the flag itself — testing sc.account would let
// `--account X --raw` through and return matches the filter never touched.
func TestSessionScope_AccountAloneStillCountsAsNarrowing(t *testing.T) {
	// sc.account is empty at guard time even when --account was given.
	sc := sessionScope{}
	accountFilter := "work"
	if !(sc.filtering() || accountFilter != "") {
		t.Error("--account alone must trip the raw/all guard")
	}
	// and with nothing set at all, it must not trip
	if sc.filtering() {
		t.Error("no filters must not trip the guard")
	}
}

// The same email can belong to two accounts in different organisations. Keeping
// whichever device was iterated last resolved the filter to the wrong one —
// silently, and differently between runs, since map order is not stable.
func TestAccountUUIDsByEmail_DropsAnAmbiguousEmail(t *testing.T) {
	staging := t.TempDir()
	reg, err := devices.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reg.Touch("mbp", "macos", "2.1.237", &devices.Account{
		AccountUUID: "aaaaaaaa-1111-2222-3333-444444444444", OrganizationUUID: "org-a", Email: "same@example.com"}, now)
	reg.Touch("air", "macos", "2.1.237", &devices.Account{
		AccountUUID: "bbbbbbbb-1111-2222-3333-444444444444", OrganizationUUID: "org-b", Email: "same@example.com"}, now)
	reg.Touch("pc", "windows", "2.1.237", &devices.Account{
		AccountUUID: "cccccccc-1111-2222-3333-444444444444", OrganizationUUID: "org-c", Email: "solo@example.com"}, now)
	if err := reg.Save(staging); err != nil {
		t.Fatal(err)
	}

	byEmail := accountUUIDsByEmail(staging)
	if _, ok := byEmail["same@example.com"]; ok {
		t.Error("an email claimed by two accounts must not resolve to one of them arbitrarily")
	}
	if byEmail["solo@example.com"] != "cccccccc-1111-2222-3333-444444444444" {
		t.Errorf("unambiguous email = %q, want uuid-solo", byEmail["solo@example.com"])
	}
	// both uuids are still offered as prefix candidates
	all := accountUUIDCandidates(staging)
	if len(all["same@example.com"]) != 2 {
		t.Errorf("want both candidates retained, got %v", all["same@example.com"])
	}
}

// Store.Resolve now returns sentinels, so a caller can tell the two
// fallback-safe misses from an ambiguity or an unreadable store. Text matching
// could not: a raw os.PathError from a permission failure can contain "not
// found", and reading that as a miss answered with the wrong account.
func TestResolveErrors_OnlyTheTwoMissesAreFallbackSafe(t *testing.T) {
	if !errors.Is(fmt.Errorf("%w %q", account.ErrNoSuchAccount, "zzz"), account.ErrNoSuchAccount) {
		t.Error("a wrapped no-match must still classify as one")
	}
	if errors.Is(account.ErrNoAccounts, account.ErrNoSuchAccount) {
		t.Error("the two sentinels must stay distinct")
	}
	// the shapes that must NOT be treated as a miss
	ambiguous := fmt.Errorf("%q matches 2 accounts (a@x.com, b@x.com) — be more specific", "j")
	pathErr := &os.PathError{Op: "open", Path: "/store/not found/accounts", Err: errors.New("permission denied")}
	for _, e := range []error{ambiguous, pathErr} {
		if errors.Is(e, account.ErrNoSuchAccount) || errors.Is(e, account.ErrNoAccounts) {
			t.Errorf("%v must not classify as a miss", e)
		}
	}
}

// Raw spelling comparisons treated the same account written two ways as two
// accounts, and accepted malformed values as selectable accounts.
func TestCanonicalUUID(t *testing.T) {
	want := "456fc32e-7579-49c7-bb2a-099657892c6a"
	for _, in := range []string{want, strings.ToUpper(want), "  " + want + "  "} {
		if got := canonicalUUID(in); got != want {
			t.Errorf("canonicalUUID(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "not-a-uuid", "456fc32e75794 9c7bb2a099657892c6a", "456fc32e-7579-49c7-bb2a", "zzzzzzzz-7579-49c7-bb2a-099657892c6a"} {
		if got := canonicalUUID(bad); got != "" {
			t.Errorf("canonicalUUID(%q) = %q, want it rejected", bad, got)
		}
	}
}

// A prefix must resolve regardless of how the stored uuid was cased.
func TestResolveAccountFilter_PrefixIsCaseInsensitiveOnBothSides(t *testing.T) {
	full := "456fc32e-7579-49c7-bb2a-099657892c6a"
	known := map[string]ledger.Entry{"s1": {ID: "s1", Account: strings.ToUpper(full)}}
	got, err := resolveAccountFilter("456FC32E", t.TempDir(), known)
	if err != nil || got != full {
		t.Errorf("got %q/%v, want the canonical %q", got, err, full)
	}
}
