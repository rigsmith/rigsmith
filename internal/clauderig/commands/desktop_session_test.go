package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// dirsOf mirrors Store.CandidateDataDirs for a set of loaded profiles.
// mustFind is findSessions for tests: a scan error is a test failure, never a
// silent empty result.
func mustFind(t *testing.T, ref, home string, idx session.Index) []sessionCandidate {
	t.Helper()
	got, err := findSessions(ref, home, idx)
	if err != nil {
		t.Fatalf("findSessions(%q): %v", ref, err)
	}
	return got
}

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

	got := mustFind(t, id, home, session.Index{})
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("uuid should resolve to itself: %+v", got)
	}
	if got[0].Title != "the auth refactor" {
		t.Errorf("title should fall back to the first prompt, got %q", got[0].Title)
	}

	// present in the repo but not here: Desktop could not read it
	absent := mustFind(t, "00000000-0000-0000-0000-000000000000", home, session.Index{})
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

	if got := mustFind(t, "auth refactor", home, idx); len(got) != 2 {
		t.Errorf("both the sidecar title and the first prompt should match, got %d", len(got))
	}
	got := mustFind(t, "planning", home, idx)
	if len(got) != 1 || got[0].ID != withSidecar {
		t.Errorf("sidecar-title match = %+v", got)
	}
	if none := mustFind(t, "nothing matches this", home, idx); len(none) != 0 {
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
	got, err := otherRunningWindows(onlyTarget, dirsOf(profiles), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("only the target running: want none, got %v", got)
	}

	both := stubApp{open: map[string]bool{target.DataDir(): true, other.DataDir(): true}}
	got, err = otherRunningWindows(both, dirsOf(profiles), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != other.Name {
		t.Fatalf("want [%s], got %v", other.Name, got)
	}

	err = ambiguousRoutingError(st, target, got)
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
func TestOtherRunningWindows_IncludesTheDefaultInstall(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil || len(profiles) != 2 {
		t.Fatalf("store setup: %v %d", err, len(profiles))
	}
	target := profiles[0]

	// Only the target profile, but the profile-less app is up.
	app := stubApp{open: map[string]bool{target.DataDir(): true, "__default__": true}}
	got, gerr := otherRunningWindows(app, dirsOf(profiles), target)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if len(got) != 1 || got[0] != defaultInstanceLabel {
		t.Fatalf("want [%s], got %v", defaultInstanceLabel, got)
	}

	// It cannot be quit by name, so the remedy must not offer a quit command
	// naming it — that would be an instruction that fails.
	err = ambiguousRoutingError(st, target, got)
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
func TestOtherRunningWindows_FailsClosedOnScanError(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherRunningWindows(scanFailApp{}, dirsOf(profiles), profiles[0]); err == nil {
		t.Fatal("a failed profile scan must be an error, not an empty result")
	}
	if _, err := otherRunningWindows(defaultScanFailApp{}, dirsOf(profiles), profiles[0]); err == nil {
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
	got := mustFind(t, "Git/api", home, session.Index{})
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

	err = ambiguousRoutingError(st, target, []string{other.Name, defaultInstanceLabel})
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
func TestOtherRunningWindows_SeesAProfileWithUnreadableMetadata(t *testing.T) {
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
	others, err := otherRunningWindows(app, dirs, target)
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
		got := mustFind(t, ref, home, session.Index{})
		if len(got) != 1 {
			t.Fatalf("%s: got %d candidates, want 1", ref, len(got))
		}
		if got[0].ID != lower {
			t.Errorf("%s: ID = %q, want the canonical lowercase id", ref, got[0].ID)
		}
	}
}

// The routing scan deliberately includes directories whose metadata will not
// parse, and a profile name may contain a space. Emitting every candidate into
// a quit command produces instructions that fail — `quit broken` cannot
// resolve, `quit has space` arrives as two arguments.
func TestAmbiguousRoutingError_OnlyOffersQuitForRealProfiles(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	target, real := profiles[0], profiles[1]

	err = ambiguousRoutingError(st, target, []string{real.Name, "broken", "has space"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, "desktop quit "+real.Name) {
		t.Errorf("a real profile should be quittable by name: %v", msg)
	}
	for _, bad := range []string{"quit broken", "quit has space"} {
		if strings.Contains(msg, bad) {
			t.Errorf("offered an unusable command %q: %v", bad, msg)
		}
	}
	for _, named := range []string{"broken", "has space"} {
		if !strings.Contains(msg, named) {
			t.Errorf("candidate %q should still be named for manual closing: %v", named, msg)
		}
	}
}

// A whitespace-only --session is not a request. Untrimmed it enabled the whole
// path, then resolved an empty needle that matches every session.
func TestFindSessions_EmptyNeedleIsNotAWildcard(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, "-Users-j-Git-api", "11111111-1111-1111-1111-111111111111", "one")
	writeTranscript(t, home, "-Users-j-Git-api", "22222222-2222-2222-2222-222222222222", "two")

	// what the command now passes after trimming
	if got := mustFind(t, "", home, session.Index{}); len(got) != 0 {
		t.Errorf("an empty reference matched %d sessions; it must match none", len(got))
	}
}

// A Desktop window launched with a --user-data-dir OUTSIDE clauderig's store is
// invisible to any profile enumeration — it has the flag, so it is not the
// profile-less install, and it is not in the store, so no listing names it. It
// still competes for a scheme-routed deep link.
func TestOtherRunningWindows_SeesAnUnmanagedProfile(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	target := profiles[0]
	dirs, err := st.CandidateDataDirs()
	if err != nil {
		t.Fatal(err)
	}

	app := stubApp{open: map[string]bool{
		target.DataDir():       true,
		"/somewhere/else/data": true, // never heard of by the store
	}}
	got, err := otherRunningWindows(app, dirs, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the unmanaged window counted, got %v", got)
	}
	if !strings.Contains(got[0], "/somewhere/else/data") {
		t.Errorf("an unnamed window should be described by what is known: %q", got[0])
	}
}

// The store entry and the running process can spell the same directory
// differently — a symlinked profile directory, or a case-insensitive
// filesystem. Comparing raw strings made one window look like two and refused a
// send when only the target was open.
func TestOtherRunningWindows_MatchesTheTargetThroughASymlink(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	target := profiles[0]
	dirs, err := st.CandidateDataDirs()
	if err != nil {
		t.Fatal(err)
	}

	// the process reports the path by another name
	alias := filepath.Join(t.TempDir(), "aliased-data")
	if err := os.MkdirAll(target.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target.DataDir(), alias); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	got, err := otherRunningWindows(stubApp{open: map[string]bool{alias: true}}, dirs, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the target's own window was counted as a competitor: %v", got)
	}
}

// A failed process scan is not an empty one — the guard decides whether it is
// safe to send on this answer.
func TestOtherRunningWindows_FailsClosed(t *testing.T) {
	st := targetStore(t)
	profiles, _ := st.List()
	dirs, _ := st.CandidateDataDirs()
	if _, err := otherRunningWindows(scanFailApp{}, dirs, profiles[0]); err == nil {
		t.Fatal("a failed scan must be an error, not an empty result")
	}
}

// LastActivity is zero for the ~97% of sessions with no Desktop sidecar, so
// comparing it first made any sidecar timestamp beat every transcript-only
// session regardless of true recency. "Newest first" meant "sidecar first".
func TestFindSessions_NewestFirstAcrossSidecarAndTranscriptOnly(t *testing.T) {
	home := t.TempDir()
	oldSidecar := "11111111-1111-1111-1111-111111111111"
	newCLIOnly := "22222222-2222-2222-2222-222222222222"
	writeTranscript(t, home, "-Users-j-Git-api", oldSidecar, "auth work one")
	writeTranscript(t, home, "-Users-j-Git-api", newCLIOnly, "auth work two")

	// the sidecar-backed session is genuinely OLDER
	old := time.Now().Add(-72 * time.Hour)
	oldPath := filepath.Join(home, "projects", "-Users-j-Git-api", oldSidecar+".jsonl")
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	idx := session.Index{oldSidecar: {ID: oldSidecar, Title: "auth work one", LastActivity: old}}

	got := mustFind(t, "auth work", home, idx)
	if len(got) != 2 {
		t.Fatalf("want both, got %d", len(got))
	}
	if got[0].ID != newCLIOnly {
		t.Errorf("first = %s, want the genuinely newer transcript-only session", got[0].ID)
	}
}

// projects/ can hold .jsonl files that are not sessions. A non-uuid stem
// reaching the deep link is silently dropped by Desktop — the command reports
// success and nothing opens.
func TestFindSessions_IgnoresNonSessionTranscripts(t *testing.T) {
	home := t.TempDir()
	writeTranscript(t, home, "-Users-j-Git-api", "not-a-session-id", "auth notes")
	writeTranscript(t, home, "-Users-j-Git-api", "33333333-3333-3333-3333-333333333333", "auth notes")

	got := mustFind(t, "auth notes", home, session.Index{})
	if len(got) != 1 {
		t.Fatalf("want only the real session, got %d: %+v", len(got), got)
	}
	if !sessionUUID.MatchString(got[0].ID) {
		t.Errorf("candidate id %q is not a session id", got[0].ID)
	}
}

// A sidecar read from the SYNCED tree carries a portable "$HOME/..." template,
// not a path on this machine — so matching against it misses every search using
// the real path, and displaying it shows a directory that does not exist here.
func TestCwdFor_PrefersTheTranscriptOverATemplatedSidecar(t *testing.T) {
	home := t.TempDir()
	id := "44444444-4444-4444-4444-444444444444"
	writeTranscript(t, home, "-Users-j-Git-api", id, "hello")
	path := filepath.Join(home, "projects", "-Users-j-Git-api", id+".jsonl")

	templated := session.Meta{ID: id, Cwd: "$HOME/Git/api"}
	if got := cwdFor(templated, path); got != "/Users/j/Git/api" {
		t.Errorf("cwdFor = %q, want the transcript's real path", got)
	}
	// a real sidecar path is still preferred
	real := session.Meta{ID: id, Cwd: "/Users/someone/Git/api"}
	if got := cwdFor(real, path); got != "/Users/someone/Git/api" {
		t.Errorf("cwdFor = %q, want the sidecar's own path", got)
	}
}

// The ambiguity listing suggests a retry, so the ids on it have to be usable —
// a short id cannot be passed back to --session.
func TestPickSession_ListsFullIDs(t *testing.T) {
	full := "55555555-5555-5555-5555-555555555555"
	_, err := pickSession("auth", []sessionCandidate{
		{ID: full, Title: "one"},
		{ID: "66666666-6666-6666-6666-666666666666", Title: "two"},
	})
	if err == nil {
		t.Fatal("want an ambiguity error")
	}
	if !strings.Contains(err.Error(), full) {
		t.Errorf("listing must print ids that can be re-run: %v", err)
	}
}

// The target is excluded by PID. A flattened command line cannot be split back
// into arguments — given "--user-data-dir=/Users/Jane -- Doe/data", no rule
// decides whether the value ends at "Jane" or at "data" — and guessing wrong in
// the permissive direction skips a window that is NOT the target, undercounting
// the competitors the guard exists to find.
func TestOtherRunningWindows_ExcludesTheTargetByPID(t *testing.T) {
	st := targetStore(t)
	profiles, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	target, other := profiles[0], profiles[1]
	dirs, err := st.CandidateDataDirs()
	if err != nil {
		t.Fatal(err)
	}

	app := stubApp{open: map[string]bool{target.DataDir(): true, other.DataDir(): true}}
	got, err := otherRunningWindows(app, dirs, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != other.Name {
		t.Fatalf("want [%s], got %v", other.Name, got)
	}

	// and a failed scan for the target is an error, not an empty exclusion set
	if _, err := otherRunningWindows(scanFailApp{}, dirs, target); err == nil {
		t.Error("a failed target scan must be an error")
	}
}
