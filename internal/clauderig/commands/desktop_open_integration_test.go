package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// recordingApp is a stubApp that remembers the deep links handed to it, which
// is the one thing the command's unit-tested pieces cannot observe: whether the
// session it resolved is ever actually SENT.
type recordingApp struct {
	stubApp
	urls *[]string
}

func (r recordingApp) OpenURL(u string) error {
	*r.urls = append(*r.urls, u)
	return nil
}

// Launching marks the profile running, as a real launch would. Without it the
// command waits out desktopReadyTimeout and refuses to send — correctly, since
// a deep link sent before the window exists is routed to the wrong install.
func (r recordingApp) Launch(dataDir string) error {
	r.open[dataDir] = true
	return nil
}

// wireDesktop points the command at a temporary store, a fake app, and a
// temporary ~/.claude — so `desktop open` can be run end to end without an
// Electron binary or the developer's own sessions.
func wireDesktop(t *testing.T, open map[string]bool) (*desktop.Store, *[]string, string) {
	t.Helper()
	st := desktop.NewStore(filepath.Join(t.TempDir(), "desktop"))
	if _, err := st.Create("work", "work@example.com", ""); err != nil {
		t.Fatal(err)
	}
	urls := &[]string{}
	app := recordingApp{stubApp: stubApp{open: open}, urls: urls}

	oldStore, oldApp := desktopStore, newDesktopApp
	desktopStore = func() (*desktop.Store, error) { return st, nil }
	newDesktopApp = func() desktop.App { return app }
	t.Cleanup(func() { desktopStore, newDesktopApp = oldStore, oldApp })

	// account.ClaudeHome resolves os.UserHomeDir()/.claude, so the home itself is
	// what has to move. USERPROFILE as well as HOME, because that is the one
	// UserHomeDir reads on Windows and this test is not build-tagged.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return st, urls, filepath.Join(home, ".claude")
}

// The regression this PR shipped: `-i` resolved a session through the picker
// and then returned without sending it, because the early returns asked whether
// a --session REFERENCE was given rather than whether a session was chosen. A
// unit test on pickSession passes either way; only driving the command catches
// it.
func TestDesktopOpen_SessionIsActuallySent(t *testing.T) {
	_, urls, home := wireDesktop(t, map[string]bool{})

	id := "a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeTranscript(t, home, "-Users-j-Git-acme-api", id, "auth refactor")

	cmd := newDesktopOpenCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"work", "--session", id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(*urls) != 1 {
		t.Fatalf("want exactly one deep link sent, got %v", *urls)
	}
	if want := "claude://resume?session=" + id; (*urls)[0] != want {
		t.Errorf("sent %q, want %q", (*urls)[0], want)
	}
}

// The same guard on the already-open branch, which is the common case: the
// window is up, the command focuses it, and it must still send.
func TestDesktopOpen_SentToAnAlreadyOpenWindow(t *testing.T) {
	st, urls, home := wireDesktop(t, map[string]bool{})
	work, err := st.Resolve("work")
	if err != nil {
		t.Fatal(err)
	}

	id := "a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeTranscript(t, home, "-Users-j-Git-acme-api", id, "auth refactor")

	cmd := newDesktopOpenCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"work", "--session", id})
	// Already running, so this takes the Focus path and never waits.
	newDesktopApp().(recordingApp).open[work.DataDir()] = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(*urls) != 1 {
		t.Fatalf("focusing an open window must still send the session, got %v", *urls)
	}
}

// The other half of the same guard: with no session asked for, the command must
// open the window and send NOTHING. Keying the early return on the resolved
// session rather than the reference must not turn a plain `open` into a send.
func TestDesktopOpen_WithoutASessionSendsNothing(t *testing.T) {
	_, urls, _ := wireDesktop(t, map[string]bool{})

	cmd := newDesktopOpenCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*urls) != 0 {
		t.Errorf("a plain open must send no deep link, got %v", *urls)
	}
}

// A session that cannot be resolved must cost nothing: no window opened, no
// link sent. The resolution deliberately happens before any app call.
func TestDesktopOpen_UnresolvableSessionTouchesNothing(t *testing.T) {
	_, urls, _ := wireDesktop(t, map[string]bool{})

	cmd := newDesktopOpenCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"work", "--session", "nothing-matches-this"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("want an error for a session that matches nothing")
	}
	if !strings.Contains(err.Error(), "no session on this machine matches") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(*urls) != 0 {
		t.Errorf("nothing should have been sent, got %v", *urls)
	}
}

// The exact regression: `-i` with no `--session`. The picker needs a terminal,
// so the selection is stubbed — what is under test is the WIRING after it, which
// is where the bug lived. Guarded with sessionRef this fails, because `-i`
// leaves the reference empty by design.
func TestDesktopOpen_InteractivePickIsSent(t *testing.T) {
	st, urls, home := wireDesktop(t, map[string]bool{})
	work, err := st.Resolve("work")
	if err != nil {
		t.Fatal(err)
	}
	id := "a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeTranscript(t, home, "-Users-j-Git-acme-api", id, "auth refactor")

	old := selectSession
	var sawRef string
	var sawForce bool
	selectSession = func(ref string, cands []sessionCandidate, force bool) (sessionCandidate, error) {
		sawRef, sawForce = ref, force
		if len(cands) == 0 {
			t.Error("the picker was offered no candidates")
			return sessionCandidate{}, nil
		}
		return cands[0], nil
	}
	t.Cleanup(func() { selectSession = old })

	cmd := newDesktopOpenCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"work", "-i"})
	newDesktopApp().(recordingApp).open[work.DataDir()] = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if sawRef != "" || !sawForce {
		t.Errorf("picker got ref=%q force=%v, want an empty ref and force", sawRef, sawForce)
	}
	if len(*urls) != 1 {
		t.Fatalf("the chosen session must be sent, got %v", *urls)
	}
	if want := "claude://resume?session=" + id; (*urls)[0] != want {
		t.Errorf("sent %q, want %q", (*urls)[0], want)
	}
}
