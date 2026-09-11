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

// raiseApp records which window `desktop open` brought forward.
type raiseApp struct {
	stubApp
	raised      []int
	focused     []string
	raiseErr    error
	runningPIDs []int
}

func (r *raiseApp) Running(string) ([]int, error) { return r.runningPIDs, nil }
func (r *raiseApp) Raise(pid int) error {
	r.raised = append(r.raised, pid)
	return r.raiseErr
}
func (r *raiseApp) Focus(dir string) error {
	r.focused = append(r.focused, dir)
	return nil
}

// Focus raises the APPLICATION, and every instance is one application to the
// OS — so with two profiles open it could put the wrong window in front and
// report success. The pid is the only thing that names one window.
func TestOpenRaisesTheProfilesOwnWindow(t *testing.T) {
	app := &raiseApp{runningPIDs: []int{5150}}
	p := desktop.Profile{Name: "work"}
	if err := raiseOrFocus(app, p); err != nil {
		t.Fatal(err)
	}
	if len(app.raised) != 1 || app.raised[0] != 5150 {
		t.Errorf("raised %v, want the running window's pid", app.raised)
	}
	if len(app.focused) != 0 {
		t.Errorf("fell back to focusing the app: %v", app.focused)
	}
}

// Where naming one window is impossible, activating the app is imprecise
// rather than wrong — so it is still done.
func TestOpenFallsBackToFocusWhereRaisingIsUnsupported(t *testing.T) {
	app := &raiseApp{runningPIDs: []int{5150}, raiseErr: desktop.ErrRaiseUnsupported}
	if err := raiseOrFocus(app, desktop.Profile{Name: "work"}); err != nil {
		t.Fatal(err)
	}
	if len(app.focused) != 1 {
		t.Errorf("focused %v, want the fallback to have run", app.focused)
	}
}

// A refused Automation prompt is not the same as a platform that cannot raise
// windows: falling back would raise some window or other and call it success.
func TestOpenReportsARefusedRaiseRatherThanFallingBack(t *testing.T) {
	app := &raiseApp{runningPIDs: []int{5150}, raiseErr: errors.New("not authorized to send Apple events")}
	if err := raiseOrFocus(app, desktop.Profile{Name: "work"}); err == nil {
		t.Fatal("a refused raise was reported as success")
	}
	if len(app.focused) != 0 {
		t.Errorf("fell back after a refusal: %v", app.focused)
	}
}

// A pid that died between the scan and the raise must not stop the search: the
// point of raising is to reach a live window, and another instance of the same
// app may be sitting right there.
func TestRaiseAnySkipsAStalePidForALiveOne(t *testing.T) {
	dead := errors.New("no such process")
	app := &sequenceApp{errs: []error{dead, nil}}
	if err := raiseAny(app, []int{111, 222}); err != nil {
		t.Fatalf("gave up on a stale pid with a live window behind it: %v", err)
	}
	if len(app.tried) != 2 || app.tried[1] != 222 {
		t.Errorf("tried %v, want it to move on to the live window", app.tried)
	}
}

// When every window is gone, the last failure is the answer.
func TestRaiseAnyReportsTheLastFailureWhenNoneAnswer(t *testing.T) {
	app := &sequenceApp{errs: []error{errors.New("first"), errors.New("last")}}
	err := raiseAny(app, []int{1, 2})
	if err == nil || err.Error() != "last" {
		t.Errorf("err = %v, want the last failure", err)
	}
}

// An unsupported platform is not a per-window failure — asking again about the
// next pid is asking the same question.
func TestRaiseAnyStopsAtAnUnsupportedPlatform(t *testing.T) {
	app := &sequenceApp{errs: []error{desktop.ErrRaiseUnsupported, nil}}
	if err := raiseAny(app, []int{1, 2}); !errors.Is(err, desktop.ErrRaiseUnsupported) {
		t.Errorf("err = %v, want ErrRaiseUnsupported", err)
	}
	if len(app.tried) != 1 {
		t.Errorf("tried %v, want it to stop after the first answer", app.tried)
	}
}

// sequenceApp answers each Raise with the next error in its list.
type sequenceApp struct {
	stubApp
	errs  []error
	tried []int
}

func (s *sequenceApp) Raise(pid int) error {
	s.tried = append(s.tried, pid)
	if len(s.tried) <= len(s.errs) {
		return s.errs[len(s.tried)-1]
	}
	return nil
}
