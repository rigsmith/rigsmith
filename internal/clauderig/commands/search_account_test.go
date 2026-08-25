package commands

import (
	"testing"

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
