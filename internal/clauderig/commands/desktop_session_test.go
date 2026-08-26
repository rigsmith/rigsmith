package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// dirsOf mirrors Store.CandidateDataDirs for a set of loaded profiles.
func dirsOf(profiles []desktop.Profile) map[string]string {
	out := map[string]string{}
	for _, p := range profiles {
		out[p.Name] = p.DataDir()
	}
	return out
}

func writeTranscript(t *testing.T, home, slug, id, firstPrompt string) {
	t.Helper()
	dir := filepath.Join(home, "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","cwd":"/Users/j/Git/api","message":{"content":"` + firstPrompt + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Desktop takes exactly one parameter and requires a uuid; anything else is
// dropped with "missing or invalid session" and no visible effect.
func TestResumeDeepLink(t *testing.T) {
	got := resumeDeepLink("456fc32e-7579-49c7-bb2a-099657892c6a")
	want := "claude://resume?session=456fc32e-7579-49c7-bb2a-099657892c6a"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A uuid resolves to itself — but only if its transcript is on this machine,
// because that is where Desktop reads it from.
func TestFindSessions_UUIDRequiresALiveTranscript(t *testing.T) {
	home := t.TempDir()
	id := "456fc32e-7579-49c7-bb2a-099657892c6a"
	writeTranscript(t, home, "-Users-j-Git-api", id, "the auth refactor")

	got := findSessions(id, home, session.Index{})
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("uuid should resolve to itself: %+v", got)
	}
	if got[0].Title != "the auth refactor" {
		t.Errorf("title should fall back to the first prompt, got %q", got[0].Title)
	}

	// present in the repo but not here: Desktop could not read it
	absent := findSessions("00000000-0000-0000-0000-000000000000", home, session.Index{})
	if len(absent) != 0 {
		t.Errorf("a uuid with no live transcript must not resolve: %+v", absent)
	}
}

// Text matches the sidecar title where there is one, and the transcript's first
// prompt for the ~97% of sessions that have none.
func TestFindSessions_MatchesTitleAndFirstPrompt(t *testing.T) {
	home := t.TempDir()
	withSidecar := "11111111-1111-1111-1111-111111111111"
	cliOnly := "22222222-2222-2222-2222-222222222222"
	writeTranscript(t, home, "-Users-j-Git-api", withSidecar, "unrelated opening line")
	writeTranscript(t, home, "-Users-j-Git-api", cliOnly, "the auth refactor")

	idx := session.Index{withSidecar: {ID: withSidecar, Title: "Auth refactor planning", Cwd: "/Users/j/Git/api"}}

	if got := findSessions("auth refactor", home, idx); len(got) != 2 {
		t.Errorf("both the sidecar title and the first prompt should match, got %d", len(got))
	}
	got := findSessions("planning", home, idx)
	if len(got) != 1 || got[0].ID != withSidecar {
		t.Errorf("sidecar-title match = %+v", got)
	}
	if none := findSessions("nothing matches this", home, idx); len(none) != 0 {
		t.Errorf("want no matches, got %+v", none)
	}
}

// Off a terminal there is no picker, so several matches must fail with the ids
// needed to re-run — never silently pick one.
func TestPickSession_AmbiguousOffATerminalListsIDs(t *testing.T) {
	cands := []sessionCandidate{
		{ID: "11111111-1111-1111-1111-111111111111", Title: "one"},
		{ID: "22222222-2222-2222-2222-222222222222", Title: "two"},
	}
	_, err := pickSession("auth", cands)
	if err == nil {
		t.Fatal("ambiguity must not resolve itself")
	}
	for _, want := range []string{"2 sessions match", "11111111", "22222222"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// No match names the one thing that would explain it — the transcript is not on
// this machine — rather than just reporting nothing.
func TestPickSession_NoMatchExplainsWhy(t *testing.T) {
	_, err := pickSession("auth", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "~/.claude/projects") {
		t.Errorf("error should say where Desktop reads transcripts from: %v", err)
	}
}

func TestPickSession_SingleMatchNeedsNoPrompt(t *testing.T) {
	only := sessionCandidate{ID: "11111111-1111-1111-1111-111111111111", Title: "one"}
	got, err := pickSession("auth", []sessionCandidate{only})
	if err != nil || got.ID != only.ID {
		t.Fatalf("got %+v/%v", got, err)
	}
}

// A deep link is routed by scheme, not per window. Observed live: asking for
// one profile with another open imported the session into the OTHER account.
// That crosses an account boundary, so it is refused rather than warned about.
func TestOtherRunningProfilesAndRefusal(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil || len(profiles) != 2 {
		t.Fatalf("store setup: %v %d", err, len(profiles))
	}
	target, other := profiles[0], profiles[1]

	onlyTarget := stubApp{open: map[string]bool{target.DataDir(): true}}
	got, err := otherRunningProfiles(onlyTarget, dirsOf(profiles), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("only the target running: want none, got %v", got)
	}

	both := stubApp{open: map[string]bool{target.DataDir(): true, other.DataDir(): true}}
	got, err = otherRunningProfiles(both, dirsOf(profiles), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != other.Name {
		t.Fatalf("want [%s], got %v", other.Name, got)
	}

	err = ambiguousRoutingError(target, got)
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{other.Name, "wrong account", "--anyway", "clauderig desktop quit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

// The ordinary Claude Desktop carries no --user-data-dir, so no profile scan
// can see it — yet it competes for the deep link exactly like a profile. Left
// out, the refusal under-reports and the session can still cross an account.
func TestOtherRunningProfiles_IncludesTheDefaultInstall(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil || len(profiles) != 2 {
		t.Fatalf("store setup: %v %d", err, len(profiles))
	}
	target := profiles[0]

	// Only the target profile, but the profile-less app is up.
	app := stubApp{open: map[string]bool{target.DataDir(): true, "__default__": true}}
	got, gerr := otherRunningProfiles(app, dirsOf(profiles), target)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if len(got) != 1 || got[0] != defaultInstanceLabel {
		t.Fatalf("want [%s], got %v", defaultInstanceLabel, got)
	}

	// It cannot be quit by name, so the remedy must not offer a quit command
	// naming it — that would be an instruction that fails.
	err = ambiguousRoutingError(target, got)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if strings.Contains(err.Error(), "desktop quit "+defaultInstanceLabel) {
		t.Errorf("must not tell the user to quit a non-profile by name: %v", err)
	}
	if !strings.Contains(err.Error(), defaultInstanceLabel) {
		t.Errorf("refusal should name it: %v", err)
	}
}

// "Could not look" is not "nothing is open". A failed scan must stop the send,
// or the refusal passes on unknown state and the session can still cross an
// account — the exact outcome this guard exists to prevent.
func TestOtherRunningProfiles_FailsClosedOnScanError(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherRunningProfiles(scanFailApp{}, dirsOf(profiles), profiles[0]); err == nil {
		t.Fatal("a failed profile scan must be an error, not an empty result")
	}
	if _, err := otherRunningProfiles(defaultScanFailApp{}, dirsOf(profiles), profiles[0]); err == nil {
		t.Fatal("a failed scan for the profile-less app must be an error too")
	}
}

// A title is arbitrary user text. Byte slicing would cut a multi-byte character
// in half and print mojibake.
func TestSessionCandidate_LabelTruncatesByRunes(t *testing.T) {
	long := strings.Repeat("é", 80) // 2 bytes each: byte-slicing splits one
	c := sessionCandidate{ID: "x", Title: long}
	got := c.label()
	if !utf8.ValidString(got) {
		t.Errorf("label is not valid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) != 58 { // 57 kept + the ellipsis
		t.Errorf("label = %d runes, want 58", len(r))
	}
}

// The project shown for a session must also be searchable. Matching the sidecar
// cwd alone would search only the ~3% of sessions that have one, while listing a
// project for all of them.
func TestFindSessions_MatchesProjectOfATranscriptOnlySession(t *testing.T) {
	home := t.TempDir()
	id := "33333333-3333-3333-3333-333333333333"
	writeTranscript(t, home, "-Users-j-Git-api", id, "nothing quotable here")

	// no sidecar at all — cwd can only come from the transcript
	got := findSessions("Git/api", home, session.Index{})
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("want the transcript-only session matched by project, got %+v", got)
	}
}

// A mixed conflict — a named profile AND the profile-less app — must tell the
// user about both. Naming only the quit command leaves the main app open, so
// the re-run it instructs lands on this same refusal.
func TestAmbiguousRoutingError_MixedConflictNamesBothRemedies(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	target, other := profiles[0], profiles[1]

	err = ambiguousRoutingError(target, []string{other.Name, defaultInstanceLabel})
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, "desktop quit "+other.Name) {
		t.Errorf("should name the quit command for the profile: %v", msg)
	}
	if !strings.Contains(msg, "close "+defaultInstanceLabel) {
		t.Errorf("should tell the user to close the main app by hand: %v", msg)
	}
	// staticcheck ST1005: error strings must not end in punctuation.
	if strings.HasSuffix(msg, ".") {
		t.Errorf("error string ends with punctuation: %q", msg)
	}
}

// Store.List skips a profile whose profile.json will not parse — reasonable for
// a listing, wrong for a safety scan: that profile can still be RUNNING and
// competing for the deep link. CandidateDataDirs sees it, so the scan does too.
func TestOtherRunningProfiles_SeesAProfileWithUnreadableMetadata(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil || len(profiles) != 2 {
		t.Fatalf("store setup: %v %d", err, len(profiles))
	}
	target := profiles[0]

	// A directory that looks like a profile but whose metadata is corrupt.
	broken := filepath.Join(st.Root, "broken")
	if err := os.MkdirAll(filepath.Join(broken, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "profile.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.List(); len(got) != 2 {
		t.Fatalf("List should still skip it, got %d", len(got))
	}

	dirs, err := st.CandidateDataDirs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dirs["broken"]; !ok {
		t.Fatal("CandidateDataDirs must include the unreadable profile")
	}

	// It is running: the scan has to report it, or the guard sends blind.
	app := stubApp{open: map[string]bool{filepath.Join(broken, "data"): true}}
	others, err := otherRunningProfiles(app, dirs, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != 1 || others[0] != "broken" {
		t.Errorf("want [broken], got %v", others)
	}
}

// sessionUUID accepts uppercase hex, mirroring Claude Desktop's own validator,
// but transcripts are written lowercase and both maps are keyed by the on-disk
// name. Taking the exact-id branch on an uppercase uuid and then missing the
// lookup also forgoes the substring fallback, so a session sitting on disk
// reported as "no session matches".
func TestFindSessions_UppercaseUUIDResolves(t *testing.T) {
	home := t.TempDir()
	lower := "456fc32e-7579-49c7-bb2a-099657892c6a"
	writeTranscript(t, home, "-Users-j-Git-api", lower, "the auth refactor")

	for _, ref := range []string{
		lower,
		"456FC32E-7579-49C7-BB2A-099657892C6A",
		"456Fc32E-7579-49c7-BB2a-099657892C6A",
	} {
		got := findSessions(ref, home, session.Index{})
		if len(got) != 1 {
			t.Fatalf("%s: got %d candidates, want 1", ref, len(got))
		}
		if got[0].ID != lower {
			t.Errorf("%s: ID = %q, want the canonical lowercase id", ref, got[0].ID)
		}
	}
}
