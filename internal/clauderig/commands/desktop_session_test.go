package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

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
	if got := otherRunningProfiles(onlyTarget, profiles, target); len(got) != 0 {
		t.Errorf("only the target running: want none, got %v", got)
	}

	both := stubApp{open: map[string]bool{target.DataDir(): true, other.DataDir(): true}}
	got := otherRunningProfiles(both, profiles, target)
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
	got := otherRunningProfiles(app, profiles, target)
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
