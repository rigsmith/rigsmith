package commands

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// mainApp records which of the two opposite actions `desktop main` took.
type mainApp struct {
	stubApp
	running   []int
	scanErr   error
	raiseErr  error
	launched  int
	raisedPID []int
}

func (m *mainApp) RunningDefault() ([]int, error) { return m.running, m.scanErr }
func (m *mainApp) LaunchDefault() error           { m.launched++; return nil }
func (m *mainApp) Raise(pid int) error {
	m.raisedPID = append(m.raisedPID, pid)
	return m.raiseErr
}
func (m *mainApp) Installed() (string, bool) { return "/Applications/Claude.app", true }

// runMain drives the command against a fake app and hands back what it printed.
func runMain(t *testing.T, app desktop.App) (string, error) {
	t.Helper()
	prev := newDesktopApp
	newDesktopApp = func() desktop.App { return app }
	t.Cleanup(func() { newDesktopApp = prev })

	cmd := newDesktopMainCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.RunE(cmd, nil)
	return out.String(), err
}

// Nothing running: start it. This is the case the verb exists for — with a
// profile window up, every OS gesture for "open Claude" reaches that window
// instead.
func TestDesktopMainLaunchesWhenNoneIsRunning(t *testing.T) {
	app := &mainApp{}
	out, err := runMain(t, app)
	if err != nil {
		t.Fatal(err)
	}
	if app.launched != 1 || len(app.raisedPID) != 0 {
		t.Errorf("launched=%d raised=%v, want exactly one launch and no raise", app.launched, app.raisedPID)
	}
	if !strings.Contains(out, "opened") {
		t.Errorf("said %q, want it to say it opened the app", out)
	}
}

// Already running: raise it, and do NOT launch. A second launch would put two
// machine-wide windows on one data directory.
func TestDesktopMainRaisesRatherThanLaunchingASecondCopy(t *testing.T) {
	app := &mainApp{running: []int{4321}}
	out, err := runMain(t, app)
	if err != nil {
		t.Fatal(err)
	}
	if app.launched != 0 {
		t.Error("launched a second machine-wide window over one that was already open")
	}
	if len(app.raisedPID) != 1 || app.raisedPID[0] != 4321 {
		t.Errorf("raised %v, want the running pid", app.raisedPID)
	}
	if !strings.Contains(out, "brought forward") {
		t.Errorf("said %q, want it to say it brought the window forward", out)
	}
}

// A platform that cannot raise a named window is not a failure: the window the
// user wants is there, and saying so beats an error about the window they are
// trying to reach.
func TestDesktopMainReportsAnUnraisableWindowAsOpen(t *testing.T) {
	app := &mainApp{running: []int{777}, raiseErr: desktop.ErrRaiseUnsupported}
	out, err := runMain(t, app)
	if err != nil {
		t.Fatalf("an already-open window reported as an error: %v", err)
	}
	if !strings.Contains(out, "already open") || !strings.Contains(out, "777") {
		t.Errorf("said %q, want it to say the window is open and name the pid", out)
	}
}

// A scan that failed is not "nothing is running" — launching on that answer is
// how the second window appears.
func TestDesktopMainRefusesToActOnAFailedScan(t *testing.T) {
	app := &mainApp{scanErr: errors.New("pgrep exploded")}
	_, err := runMain(t, app)
	if err == nil {
		t.Fatal("acted on a scan that failed")
	}
	if app.launched != 0 {
		t.Error("launched anyway after a failed scan")
	}
}

// Every path says what it opened. The window is indistinguishable from a
// profile's on screen, and the difference only shows up later.
func TestDesktopMainAlwaysSaysItIsNotAProfile(t *testing.T) {
	for name, app := range map[string]desktop.App{
		"launched": &mainApp{},
		"raised":   &mainApp{running: []int{1}},
		"open":     &mainApp{running: []int{1}, raiseErr: desktop.ErrRaiseUnsupported},
	} {
		out, err := runMain(t, app)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out, "not a clauderig profile") {
			t.Errorf("%s: said %q, want the warning", name, out)
		}
	}
}
