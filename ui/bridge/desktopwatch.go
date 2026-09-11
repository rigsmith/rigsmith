package bridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
)

// DesktopWindow is one running Claude Desktop main process, as the notice
// lists it.
type DesktopWindow struct {
	PID int `json:"pid"`
	// Profile is the clauderig-managed profile this window is running on, when
	// the data directory matches one. Empty for the machine-wide install and
	// for a window launched against a directory outside the store.
	Profile string `json:"profile,omitempty"`
	DataDir string `json:"dataDir,omitempty"`
	// Main marks the machine-wide install: the one started with no
	// --user-data-dir at all, which is what launching Claude from the Dock,
	// Spotlight or the Start menu gives you.
	Main bool `json:"main"`
}

// DesktopView is which Claude Desktop windows are open right now.
//
// It counts PROCESSES rather than asking the store which profiles it knows
// about, for the same reason the routing guard behind `desktop send` does: a
// window either exists or it does not, while a list of profiles can miss one
// whose metadata will not parse, one launched against a directory outside the
// store, and the machine-wide install that has no profile at all.
type DesktopView struct {
	// MainOpen is the whole point: the machine-wide Claude Desktop has a window
	// up.
	MainOpen bool            `json:"mainOpen"`
	Windows  []DesktopWindow `json:"windows"`
	// Profiles names the managed profiles with a window up, so the notice can
	// say what the main app is competing with.
	Profiles []string `json:"profiles"`
	// Managed is how many profiles the store holds. With none, there is nothing
	// for the main app to take a deep link away from and nothing worth
	// interrupting anyone over.
	Managed int `json:"managed"`
	// Error is a process scan that FAILED. It is not "nothing is running":
	// collapsing the two would let the watch report the app as closed on the
	// strength of an answer it never got.
	Error string `json:"error,omitempty"`
	// Warn is whether this machine wants the notice at all, so the page can
	// show its "don't warn again" button as already taken.
	Warn bool `json:"warn"`
	// StoreError is a profile store that could not be read. The windows are
	// still known — they come from the process list — but their names are not,
	// and Managed cannot be trusted to mean "no profiles exist".
	StoreError string `json:"storeError,omitempty"`
}

// Desktop is the read side of the Claude Desktop watch. It has no write half on
// purpose: quitting the machine-wide app is the user's own ⌘Q, and clauderig
// has no verb that reaches an instance it did not launch.
type Desktop struct {
	app desktop.App
	// dirs is Store.CandidateDataDirs behind a seam, so the watch can be tested
	// without a profile store on disk.
	dirs func() (map[string]string, error)
	// state holds the one preference this feature has: whether the notice is
	// wanted at all.
	state *uiState
}

// NewDesktop builds the service against the real app and the default store.
func NewDesktop() *Desktop {
	return &Desktop{
		state: defaultUIState(),
		app:   desktop.New(),
		dirs: func() (map[string]string, error) {
			st, err := desktop.DefaultStore()
			if err != nil {
				return nil, err
			}
			// CandidateDataDirs, not List: a profile whose profile.json will not
			// parse still has a data directory an instance can be running
			// against, and the notice is about which windows exist.
			return st.CandidateDataDirs()
		},
	}
}

// Get reports the Desktop windows open right now.
//
// Local and cheap — one process scan — which is what makes it safe on the few
// seconds' cadence a launch has to be noticed on.
func (d *Desktop) Get(ctx context.Context) (DesktopView, error) {
	var v DesktopView

	instances, err := d.app.Instances()
	if err != nil {
		v.Error = err.Error()
		return v, nil // a state to render, not a failure to raise
	}

	byDir := map[string]string{}
	dirs, derr := d.dirs()
	if derr != nil {
		v.StoreError = derr.Error()
	}
	for name, dir := range dirs {
		byDir[desktop.CanonicalDir(dir)] = name
	}
	v.Managed = len(dirs)

	for _, inst := range instances {
		row := DesktopWindow{PID: inst.PID, DataDir: inst.DataDir}
		if inst.DataDir == "" {
			row.Main = true
			v.MainOpen = true
		} else if name := byDir[desktop.CanonicalDir(inst.DataDir)]; name != "" {
			row.Profile = name
			if !slices.Contains(v.Profiles, name) {
				v.Profiles = append(v.Profiles, name)
			}
		}
		v.Windows = append(v.Windows, row)
	}
	slices.Sort(v.Profiles)
	v.Warn = d.Warn()
	return v, nil
}

// Warn reports whether the launch notice is wanted on this machine.
//
// Default yes. The notice exists because the routing hazard is invisible until
// it bites, so someone who has never expressed a preference should be told.
func (d *Desktop) Warn() bool {
	if d.state == nil {
		return true
	}
	return d.state.Bool(desktopWarnKey, true)
}

// SetWarn records whether to warn, for the tray's checkbox.
func (d *Desktop) SetWarn(on bool) error {
	if d.state == nil {
		return nil
	}
	return d.state.SetBool(desktopWarnKey, on)
}

// Mute is the notice's own "don't warn again" button.
//
// Separate from SetWarn because it is what the frontend is allowed to do: a
// window may turn its own warning off, and the way back on is the tray's
// checkbox, which is where someone who wants it back will look. An off switch
// with no visible on switch is the thing to avoid here, and it is why the tray
// item exists at all.
func (d *Desktop) Mute(ctx context.Context) error { return d.SetWarn(false) }

// Label names a window the way the tray and the notice both say it.
//
// In Go rather than only in the page, because the tray menu is built here now
// and two spellings of "the main Claude Desktop app" — one per surface — is how
// the same window ends up with two names in one product.
func (w DesktopWindow) Label() string {
	switch {
	case w.Main:
		return "the main Claude Desktop app"
	case w.Profile != "":
		return w.Profile + " (clauderig profile)"
	case w.DataDir != "":
		return "a window on " + w.DataDir
	}
	return "a Claude Desktop window"
}

// Raise brings one Claude Desktop window to the front.
//
// The pid is checked against the live process list first. It arrives from a
// menu built up to ten seconds ago, and a pid that has been recycled since
// belongs to some other program by now — raising a window that has closed
// should do nothing, not raise a stranger.
func (d *Desktop) Raise(ctx context.Context, pid int) error {
	instances, err := d.app.Instances()
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if inst.PID == pid {
			return d.app.Raise(pid)
		}
	}
	return fmt.Errorf("no Claude Desktop window with pid %d — it has closed since the menu was built", pid)
}

// OpenMain opens, or brings forward, the machine-wide Claude Desktop.
//
// Through the CLI, like every other launch this window offers: `desktop main`
// owns the scan-then-launch-or-raise decision, and a second implementation of
// "is it already running" is how two machine-wide windows end up on one history.
func (d *Desktop) OpenMain(ctx context.Context) error {
	bin, err := resolveCLI()
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, "desktop", "main").CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// Alarm is what a watch tick decided.
type Alarm int

const (
	// AlarmNone is the ordinary tick: nothing changed, or nothing is known.
	AlarmNone Alarm = iota
	// AlarmRaise means the machine-wide app has just been launched.
	AlarmRaise
	// AlarmClear means it has gone, so a notice still on screen is now stale.
	AlarmClear
)

// DesktopAlarm turns a stream of views into the two moments worth acting on.
//
// Edge-triggered, and deliberately so on three counts:
//
//   - A window ALREADY up when the UI starts is not a launch. Seeding the first
//     answer without raising means starting the tray does not greet you with a
//     notice about an app you have been using all morning.
//   - A failed scan holds the previous state. "I could not look" must never
//     read as "it closed", or the next successful scan would look like a launch
//     and the notice would appear out of nowhere.
//   - Raising only on the transition is also what makes dismissing work.
//     Nothing has to remember that you closed the notice: it will not come back
//     until the app does.
type DesktopAlarm struct {
	known bool
	open  bool
}

// Step records one view and reports what to do about it.
func (a *DesktopAlarm) Step(v DesktopView) Alarm {
	if v.Error != "" {
		return AlarmNone
	}
	was, knew := a.open, a.known
	a.open, a.known = v.MainOpen, true

	switch {
	case !knew:
		return AlarmNone
	case v.MainOpen && !was:
		// No profiles, no hazard: with nothing to route a session to, the
		// machine-wide app is simply Claude Desktop and none of clauderig's
		// business. An UNREADABLE store is the other way round — it is not
		// evidence that there are no profiles, and a warning you can dismiss
		// beats a routing failure you cannot see coming.
		if v.Managed == 0 && v.StoreError == "" {
			return AlarmNone
		}
		return AlarmRaise
	case !v.MainOpen && was:
		return AlarmClear
	}
	return AlarmNone
}
