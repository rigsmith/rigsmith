package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// fakeDesktop answers the process scan from a fixed list, so the watch can be
// tested without an Electron app on the machine running the tests.
type fakeDesktop struct {
	instances []desktop.Instance
	scanErr   error
}

func (f fakeDesktop) Instances() ([]desktop.Instance, error) { return f.instances, f.scanErr }
func (f fakeDesktop) Running(string) ([]int, error)          { return nil, f.scanErr }
func (f fakeDesktop) RunningDefault() ([]int, error)         { return nil, f.scanErr }
func (f fakeDesktop) Launch(string) error                    { return nil }
func (f fakeDesktop) Focus(string) error                     { return nil }
func (f fakeDesktop) Quit(string, time.Duration) error       { return nil }
func (f fakeDesktop) Installed() (string, bool)              { return "/Applications/Claude.app", true }
func (f fakeDesktop) OpenURL(string) error                   { return nil }

func newTestDesktop(app desktop.App, dirs map[string]string, dirsErr error) *Desktop {
	return &Desktop{app: app, dirs: func() (map[string]string, error) { return dirs, dirsErr }}
}

// The machine-wide install is the window with no --user-data-dir at all, and
// telling it from a profile is the whole question this service answers.
func TestDesktopGetSeparatesTheMainAppFromProfiles(t *testing.T) {
	app := fakeDesktop{instances: []desktop.Instance{
		{PID: 11, DataDir: ""},
		{PID: 22, DataDir: "/store/work/data"},
		{PID: 33, DataDir: "/elsewhere/data"},
	}}
	v, err := newTestDesktop(app, map[string]string{"work": "/store/work/data"}, nil).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.MainOpen {
		t.Error("a window with no --user-data-dir is the machine-wide install; MainOpen must say so")
	}
	if got, want := v.Windows, []DesktopWindow{
		{PID: 11, Main: true},
		{PID: 22, Profile: "work", DataDir: "/store/work/data"},
		{PID: 33, DataDir: "/elsewhere/data"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows = %+v, want %+v", got, want)
	}
	if got, want := v.Profiles, []string{"work"}; !reflect.DeepEqual(got, want) {
		t.Errorf("profiles = %v, want %v", got, want)
	}
	if v.Managed != 1 {
		t.Errorf("managed = %d, want 1", v.Managed)
	}
}

// A profile whose store entry is a directory symlink is the same window under
// another spelling. Failing to recognise it would show it as an unmanaged
// window on a path the user has never heard of.
func TestDesktopGetMatchesAProfileThroughASymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "data")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "work")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	app := fakeDesktop{instances: []desktop.Instance{{PID: 7, DataDir: real}}}
	v, err := newTestDesktop(app, map[string]string{"work": filepath.Join(link, "data")}, nil).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Windows[0].Profile != "work" {
		t.Errorf("profile = %q, want work — the symlinked store entry is the same directory", v.Windows[0].Profile)
	}
}

// A scan that failed is not a machine with nothing running, and the difference
// decides whether the next successful scan looks like a launch.
func TestDesktopGetReportsAFailedScanRatherThanGuessing(t *testing.T) {
	v, err := newTestDesktop(fakeDesktop{scanErr: errors.New("pgrep exploded")}, nil, nil).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Error == "" {
		t.Fatal("a failed scan must be reported, not collapsed into an empty window list")
	}
	if got := (&DesktopAlarm{known: true, open: false}).Step(v); got != AlarmNone {
		t.Errorf("alarm on a failed scan = %v, want AlarmNone", got)
	}
}

// An unreadable profile store costs the names, not the windows.
func TestDesktopGetKeepsWindowsWhenTheStoreCannotBeRead(t *testing.T) {
	app := fakeDesktop{instances: []desktop.Instance{{PID: 5, DataDir: ""}}}
	v, err := newTestDesktop(app, nil, errors.New("store unreadable")).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.MainOpen || len(v.Windows) != 1 {
		t.Errorf("windows lost with an unreadable store: %+v", v)
	}
	if v.StoreError == "" {
		t.Error("an unreadable store must be said out loud — it is why rows are unnamed")
	}
	if v.Error != "" {
		t.Errorf("a store failure is not a scan failure: Error = %q", v.Error)
	}
}

// Starting the tray while Claude Desktop is already open is not a launch.
// Raising then would greet someone with a warning about an app they have had
// open all morning.
func TestAlarmSeedsTheFirstAnswerWithoutRaising(t *testing.T) {
	var a DesktopAlarm
	if got := a.Step(DesktopView{MainOpen: true, Managed: 1}); got != AlarmNone {
		t.Errorf("first view = %v, want AlarmNone", got)
	}
	if got := a.Step(DesktopView{MainOpen: true, Managed: 1}); got != AlarmNone {
		t.Errorf("unchanged view = %v, want AlarmNone", got)
	}
}

func TestAlarmRaisesOnLaunchAndClearsOnQuit(t *testing.T) {
	var a DesktopAlarm
	closed := DesktopView{Managed: 1}
	open := DesktopView{MainOpen: true, Managed: 1}

	a.Step(closed) // seed
	if got := a.Step(open); got != AlarmRaise {
		t.Errorf("launch = %v, want AlarmRaise", got)
	}
	if got := a.Step(open); got != AlarmNone {
		t.Errorf("still open = %v, want AlarmNone — the notice must not reappear after it is dismissed", got)
	}
	if got := a.Step(closed); got != AlarmClear {
		t.Errorf("quit = %v, want AlarmClear", got)
	}
	if got := a.Step(open); got != AlarmRaise {
		t.Errorf("relaunch = %v, want AlarmRaise", got)
	}
}

// The failure this guards against: a scan error between two open ticks reads as
// "it closed", and then the next successful scan reads as a launch — a notice
// appearing out of nowhere over a window that never moved.
func TestAlarmHoldsStateAcrossAFailedScan(t *testing.T) {
	var a DesktopAlarm
	open := DesktopView{MainOpen: true, Managed: 1}

	a.Step(DesktopView{Managed: 1}) // seed: closed
	if got := a.Step(open); got != AlarmRaise {
		t.Fatalf("launch = %v, want AlarmRaise", got)
	}
	if got := a.Step(DesktopView{Error: "pgrep exploded"}); got != AlarmNone {
		t.Errorf("failed scan = %v, want AlarmNone", got)
	}
	if got := a.Step(open); got != AlarmNone {
		t.Errorf("open again after a failed scan = %v, want AlarmNone — nothing launched", got)
	}
}

// With no profiles there is nothing for the main app to take a link away from,
// so it is simply Claude Desktop and none of clauderig's business.
func TestAlarmStaysQuietWithNoProfiles(t *testing.T) {
	var a DesktopAlarm
	a.Step(DesktopView{})
	if got := a.Step(DesktopView{MainOpen: true}); got != AlarmNone {
		t.Errorf("launch with no profiles = %v, want AlarmNone", got)
	}
}

// An unreadable store is not evidence that there are no profiles. A warning
// that can be dismissed beats a routing failure nobody saw coming.
func TestAlarmWarnsWhenTheStoreCannotBeRead(t *testing.T) {
	var a DesktopAlarm
	a.Step(DesktopView{StoreError: "store unreadable"})
	if got := a.Step(DesktopView{MainOpen: true, StoreError: "store unreadable"}); got != AlarmRaise {
		t.Errorf("launch with an unreadable store = %v, want AlarmRaise", got)
	}
}
