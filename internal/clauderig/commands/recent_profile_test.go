package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

func writeSidecar(t *testing.T, base, acct, cliID, title string) {
	t.Helper()
	writeSidecarAt(t, base, acct, cliID, title, 1000)
}

func writeSidecarAt(t *testing.T, base, acct, cliID, title string, lastActivity int64) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"cliSessionId": cliID, "title": title, "lastActivityAt": lastActivity,
	})
	p := filepath.Join(base, "claude-code-sessions", acct, "org", "local_"+cliID+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A machine can carry several Claude Desktop installs. Reading only the
// machine-wide one leaves every session run in a profile with no title and
// nothing to say which app owns it — while its transcript sits in the shared
// projects tree looking like all the others.
func TestSessionIndex_ReadsEveryDesktopProfile(t *testing.T) {
	wide, profA, profB := t.TempDir(), t.TempDir(), t.TempDir()
	writeSidecar(t, wide, "acct1", "sess-wide", "Machine-wide session")
	writeSidecar(t, profA, "acct1", "sess-a", "Work profile session")
	writeSidecar(t, profB, "acct2", "sess-b", "Personal profile session")

	idx := session.Build([]session.Root{
		{Label: desktopTarget, Base: wide},
		{Label: desktopTarget, Base: profA, Profile: "work"},
		{Label: desktopTarget, Base: profB, Profile: "personal"},
	})

	for id, wantTitle := range map[string]string{
		"sess-wide": "Machine-wide session",
		"sess-a":    "Work profile session",
		"sess-b":    "Personal profile session",
	} {
		if got := idx[id].Title; got != wantTitle {
			t.Errorf("%s title = %q, want %q", id, got, wantTitle)
		}
	}
	if got := idx["sess-wide"].Profile; got != "" {
		t.Errorf("machine-wide session should carry no profile, got %q", got)
	}
	if got := idx["sess-a"].Profile; got != "work" {
		t.Errorf("profile = %q, want work", got)
	}
	if got := idx["sess-b"].Profile; got != "personal" {
		t.Errorf("profile = %q, want personal", got)
	}
}

// The same session staged in the synced repo AND live in a profile must keep the
// profile: the merge rule picks the fresher sidecar for display fields, and the
// repo copy carries no profile of its own.
func TestSessionIndex_ProfileSurvivesMerge(t *testing.T) {
	live, repo := t.TempDir(), t.TempDir()
	writeSidecar(t, live, "acct1", "sess-1", "Older title")
	writeSidecar(t, repo, "acct1", "sess-1", "Newer title")
	// Make the repo copy the fresher one.
	p := filepath.Join(repo, "claude-code-sessions", "acct1", "org", "local_sess-1.json")
	body, _ := json.Marshal(map[string]any{
		"cliSessionId": "sess-1", "title": "Newer title", "lastActivityAt": 9000,
	})
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}

	idx := session.Build([]session.Root{
		{Label: desktopTarget, Base: live, Profile: "work"},
		{Label: repoTarget, Base: repo},
	})
	m := idx["sess-1"]
	if m.Title != "Newer title" {
		t.Errorf("title = %q, want the fresher sidecar's", m.Title)
	}
	if m.Profile != "work" {
		t.Errorf("profile lost in the merge: %q", m.Profile)
	}
	if len(m.Sources) != 2 {
		t.Errorf("sources = %v, want both stores", m.Sources)
	}
}

// The client label has to distinguish three Desktop installs that all report the
// same entrypoint, or it sends you to the wrong app.
func TestClientWithProfile(t *testing.T) {
	cases := []struct {
		entrypoint, profile, want string
	}{
		{"claude-desktop", "", "desktop"},
		{"claude-desktop", "work", "desktop@work"},
		{"claude-vscode", "", "vscode"},
		{"cli", "", "cli"},
		{"sdk-cs", "", "sdk-cs"},
		{"", "work", "desktop@work"}, // unreadable transcript, sidecar still knows
		{"", "", ""},
	}
	for _, c := range cases {
		r := &sessResult{actTried: true}
		r.act.Entrypoint = c.entrypoint
		r.meta.Profile = c.profile
		if got := clientWithProfile(r); got != c.want {
			t.Errorf("entrypoint=%q profile=%q → %q, want %q", c.entrypoint, c.profile, got, c.want)
		}
	}
}

// A staged profile's Desktop tree sits one level down, under data/ — the same
// shape as the live profile. Pointing a sidecar root at desktop@<name> itself
// finds nothing and says nothing, which is exactly the silent miss this whole
// change is about.
func TestStagedProfileRootsFindSidecars(t *testing.T) {
	staging := t.TempDir()
	for _, d := range []string{"desktop", "desktop@work", "cli", "index"} {
		if err := os.MkdirAll(filepath.Join(staging, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSidecar(t, engine.StagedProfileDataDir(staging, "work"), "acct1", "sess-staged", "Staged profile session")

	names := engine.StagedProfileNames(staging)
	if strings.Join(names, ",") != "work" {
		t.Fatalf("staged profiles = %v, want [work]", names)
	}
	var roots []session.Root
	for _, n := range names {
		roots = append(roots, session.Root{
			Label: repoTarget, Base: engine.StagedProfileDataDir(staging, n), Profile: n})
	}
	idx := session.Build(roots)
	if idx["sess-staged"].Title != "Staged profile session" {
		t.Errorf("staged profile sidecar not read: %+v", idx["sess-staged"])
	}
	if idx["sess-staged"].Profile != "work" {
		t.Errorf("profile = %q, want work", idx["sess-staged"].Profile)
	}
}

// The hint has to name the one Desktop that will actually list the session,
// because no other one ever will.
func TestResumeHint_NamesTheOwningProfile(t *testing.T) {
	r := &sessResult{id: "s1"}
	r.meta.Profile = "work"
	got := resumeHint(r, "/work")
	if !strings.Contains(got, "work profile") {
		t.Errorf("hint should name the profile: %q", got)
	}
	if !strings.Contains(got, "clauderig desktop open work") {
		t.Errorf("hint should give the command to open it: %q", got)
	}
	// A live CLI transcript still gets the runnable resume: the profile only
	// decides where a DESKTOP-only session can be reopened.
	r2 := &sessResult{id: "s2", cliLive: true}
	r2.meta.Profile = "work"
	if got := resumeHint(r2, "/work"); !strings.Contains(got, "claude --resume s2") {
		t.Errorf("a resumable session must keep its command: %q", got)
	}
}

// The machine-wide install is usually the most recently touched, so it usually
// WINS the fresher-sidecar rule — and it names no profile. Reading ownership off
// the winner therefore loses the profile in the common case, which is how a
// session that plainly belongs to one Desktop ends up labelled as belonging to
// none.
func TestSessionIndex_ProfileSurvivesFresherMachineWideCopy(t *testing.T) {
	wide, prof := t.TempDir(), t.TempDir()
	writeSidecarAt(t, prof, "acct-a", "sess-1", "Older copy", 1000)
	writeSidecarAt(t, wide, "acct-a", "sess-1", "Newer copy", 9000)

	idx := session.Build([]session.Root{
		{Label: desktopTarget, Base: wide},
		{Label: desktopTarget, Base: prof, Profile: "work"},
	})
	m := idx["sess-1"]
	if m.Title != "Newer copy" {
		t.Errorf("title = %q, want the fresher copy's", m.Title)
	}
	if m.Profile != "work" {
		t.Errorf("profile = %q, want work — lost to the fresher machine-wide copy", m.Profile)
	}
	if m.Account != "acct-a" {
		t.Errorf("account = %q, want acct-a", m.Account)
	}
}

// A sidecar copied into another profile's tree keeps its own account path. The
// label must follow the account, not the directory it landed in — otherwise a
// stray copy relabels a session, and you go looking in the wrong app.
func TestReprofile_AccountBeatsTree(t *testing.T) {
	idx := session.Index{
		// Sitting in work's tree, but filed under personal's account.
		"stray": {ID: "stray", Profile: "work", Account: "UUID-PERSONAL"},
		"own":   {ID: "own", Profile: "work", Account: "uuid-work"},
	}
	reprofile(idx, map[string]string{"uuid-work": "work", "uuid-personal": "personal"})

	if got := idx["stray"].Profile; got != "personal" {
		t.Errorf("stray copy labelled %q, want personal (its account) — case must not matter", got)
	}
	if got := idx["own"].Profile; got != "work" {
		t.Errorf("own copy labelled %q, want work", got)
	}
}

// An account with no Desktop profile is the machine-wide install's. It must
// report no profile rather than inherit one from a tree it was copied into.
func TestReprofile_AccountWithNoProfileClearsTreeLabel(t *testing.T) {
	idx := session.Index{"s": {ID: "s", Profile: "work", Account: "uuid-machinewide"}}
	reprofile(idx, map[string]string{"uuid-work": "work"})
	if got := idx["s"].Profile; got != "" {
		t.Errorf("profile = %q, want empty — that account has no profile", got)
	}
}

// With nothing to resolve against, every label must be left exactly as found:
// the tree is a worse answer than the account, but it is far better than blanking
// every session on a machine whose account store cannot be read.
func TestReprofile_NoMappingLeavesLabelsAlone(t *testing.T) {
	idx := session.Index{"s": {ID: "s", Profile: "work", Account: "uuid-work"}}
	reprofile(idx, nil)
	if got := idx["s"].Profile; got != "work" {
		t.Errorf("profile = %q, want the tree fallback preserved", got)
	}
}

// A session with no account path at all (a sidecar layout we do not understand)
// keeps whatever the tree said rather than being blanked.
func TestReprofile_NoAccountKeepsTreeLabel(t *testing.T) {
	idx := session.Index{"s": {ID: "s", Profile: "work"}}
	reprofile(idx, map[string]string{"uuid-work": "work"})
	if got := idx["s"].Profile; got != "work" {
		t.Errorf("profile = %q, want work", got)
	}
}
