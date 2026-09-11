package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// cmdFor is what the process list carries for a profile window.
func cmdFor(dir string) string {
	return "/Applications/Claude.app/Contents/MacOS/Claude --user-data-dir=" + dir
}

// panelDesktop answers the scan from a fixed list and the store from a fixed
// map, so the popover's read can be tested without profiles on disk.
func panelDesktop(t *testing.T, instances []desktop.Instance, dirs map[string]string, storeErr error) *Desktop {
	t.Helper()
	d := newTestDesktop(fakeDesktop{instances: instances}, dirs, storeErr)
	return d
}

// The popover's whole job: which profiles exist, which are open, and the pid to
// raise for the ones that are.
func TestPanelListsProfilesWithTheirWindows(t *testing.T) {
	dirs := map[string]string{"work": "/store/work/data", "personal": "/store/personal/data"}
	v, err := panelDesktop(t, []desktop.Instance{
		{PID: 11, DataDir: "/store/work/data", Command: cmdFor("/store/work/data")},
	}, dirs, nil).Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]PanelProfile{}
	for _, p := range v.Profiles {
		byName[p.Name] = p
	}
	if len(byName) != 2 {
		t.Fatalf("profiles = %+v, want both from the store", v.Profiles)
	}
	if w := byName["work"]; !w.Open || w.PID != 11 {
		t.Errorf("work = %+v, want open with the pid to raise", w)
	}
	if p := byName["personal"]; p.Open || p.PID != 0 {
		t.Errorf("personal = %+v, want closed and no pid", p)
	}
	if v.MainOpen {
		t.Error("MainOpen with no profile-less window running")
	}
}

// The machine-wide app is the window with no --user-data-dir, and it is listed
// apart from the profiles because it is not one.
func TestPanelSeparatesTheMachineWideApp(t *testing.T) {
	v, err := panelDesktop(t, []desktop.Instance{
		{PID: 7, Command: "/Applications/Claude.app/Contents/MacOS/Claude"},
		{PID: 8, DataDir: "/store/work/data", Command: cmdFor("/store/work/data")},
	}, map[string]string{"work": "/store/work/data"}, nil).Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.MainOpen || v.MainPID != 7 {
		t.Errorf("main = open:%v pid:%d, want the profile-less window", v.MainOpen, v.MainPID)
	}
	if len(v.Profiles) != 1 || v.Profiles[0].PID != 8 {
		t.Errorf("profiles = %+v, want work on its own pid", v.Profiles)
	}
}

// A path a flattened command line cannot be split back into parses as empty,
// which reads as "no profile flag" — and would list somebody's work profile as
// the main app, under a row saying no account is bound to it.
func TestPanelDoesNotMistakeAnUnparseablePathForTheMainApp(t *testing.T) {
	dir := "/store/odd -- name/data"
	v, err := panelDesktop(t, []desktop.Instance{
		{PID: 21, DataDir: "", Command: cmdFor(dir)},
	}, map[string]string{"odd": dir}, nil).Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.MainOpen {
		t.Error("a profile window was listed as the machine-wide app")
	}
	if len(v.Profiles) != 1 || !v.Profiles[0].Open || v.Profiles[0].PID != 21 {
		t.Errorf("profiles = %+v, want the profile matched by its command line", v.Profiles)
	}
}

// A scan that failed is not a machine with nothing open. The profiles are still
// listed — the store knows them — but the popover has to say the states are a
// guess, or a click launches a second window on a profile already up.
func TestPanelReportsAFailedScanAndStillListsProfiles(t *testing.T) {
	d := newTestDesktop(fakeDesktop{scanErr: errors.New("pgrep exploded")},
		map[string]string{"work": "/store/work/data"}, nil)
	v, err := d.Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Error == "" {
		t.Error("a failed scan must be reported")
	}
	if len(v.Profiles) != 1 || v.Profiles[0].Open {
		t.Errorf("profiles = %+v, want them listed and none claimed open", v.Profiles)
	}
}

// An unreadable store is its own state: there are no profiles to list, and
// saying so beats an empty list that reads as "you have none".
func TestPanelReportsAnUnreadableStore(t *testing.T) {
	v, err := panelDesktop(t, nil, nil, errors.New("store unreadable")).Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Error == "" || !strings.Contains(v.Error, "store unreadable") {
		t.Errorf("Error = %q, want the store failure", v.Error)
	}
}

// The status line the popover carries is the tray's own, so the two cannot
// disagree about how the sync is doing.
func TestPanelCarriesAStatusLine(t *testing.T) {
	v, err := panelDesktop(t, nil, map[string]string{}, nil).Panel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Level == "" {
		t.Error("no level for the popover's status dot")
	}
}
