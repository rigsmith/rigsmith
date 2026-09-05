// Command ui is clauderig's ambient face: a menu bar icon that colours itself
// green/amber/red from the real sync state, plus a window for the detail.
//
// It is not a second implementation of sync. Reads import
// internal/clauderig/... in-process (see ui/bridge); anything with a side
// effect shells out to the clauderig binary, so the CLI stays the single
// implementation of everything that can lose data.
//
// See docs/CLAUDERIG-UI-PLAN.md.
package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/health"
	"github.com/rigsmith/rigsmith/ui/assets"
	"github.com/rigsmith/rigsmith/ui/bridge"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The window is plain HTML/CSS/JS on the design system's tokens — no framework
// and no bundler, which keeps CI Go-only. Revisit when the window grows past
// the status screen.
//
//go:embed all:frontend/dist
var frontend embed.FS

const (
	// AppName is the user-facing name: the macOS menu bar title, the window
	// title, the About box.
	//
	// Lowercase "c" deliberately — `claudeRig` is the wordmark the whole repo
	// and design/marks.js use, so the app matches its CLI rather than
	// introducing a second spelling of the same product.
	AppName = "claudeRig UI"

	// BundleID identifies the macOS/Windows bundle, following the same
	// dev.rigsmith.<thing> convention as the packaging examples.
	//
	// Nothing reads this yet, and being in package main nothing ever can — the
	// Info.plist and installer configs that need it aren't Go. It sits beside
	// AppName because that's where someone will look for it; README.md repeats
	// it for the packaging work to copy.
	BundleID = "dev.rigsmith.clauderig-ui"
)

// The tray's refresh cadence, and the faster one used while the window is on
// screen. status.Gather is local-only — a few git plumbing calls, no network —
// so the fast rate is affordable, and someone who has the window open is
// watching for exactly the change it would otherwise sit on for most of a
// minute. Closed, it drops back: nobody is looking, and the only consumer is
// the colour of a menu bar icon.
const (
	pollInterval = 45 * time.Second
	pollOpen     = 5 * time.Second
)

func main() {
	// Linux tray support is desktop-environment dependent — GNOME needs an
	// AppIndicator extension for the icon to appear at all. --window is the
	// escape hatch when the tray never shows up.
	showWindow := flag.Bool("window", false, "open the status window at startup instead of starting in the tray only")
	showSessions := flag.Bool("sessions", false, "open the sessions window at startup")
	flag.Parse()

	statusSvc := bridge.NewStatus()
	actionsSvc := bridge.NewActions()
	windowsSvc := bridge.NewWindows()

	app := application.New(application.Options{
		Name:        AppName,
		Description: "Claude Code sync status",
		LogLevel:    slog.LevelWarn,
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(frontend),
		},
		Services: []application.Service{
			application.NewService(statusSvc),
			application.NewService(bridge.NewActivity()),
			application.NewService(bridge.NewRepo()),
			application.NewService(bridge.NewFiling()),
			application.NewService(bridge.NewChooser()),
			application.NewService(actionsSvc),
			application.NewService(bridge.NewLibrary()),
			application.NewService(bridge.NewAccounts()),
			application.NewService(windowsSvc),
		},
		Mac: application.MacOptions{
			// Accessory keeps it out of the Dock and the app switcher: the
			// tray is the app.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	window := newWindow(app)
	sessionsWindow := newSessionsWindow(app)
	tray := newTray(app, window, sessionsWindow, actionsSvc)

	// Registered by name so the status window can raise the sessions window
	// without the frontend knowing anything about how windows are built.
	windowsSvc.Register("sessions", func() { reveal(sessionsWindow) }, func() { sessionsWindow.Hide() })
	windowsSvc.Register("main", func() { reveal(window) }, func() { window.Hide() })

	go poll(app, statusSvc, tray, window)

	// An action changes exactly what the tray reports, so repaint the moment
	// one finishes rather than waiting out the poll interval.
	app.Event.On(bridge.EventActionDone, func(*application.CustomEvent) {
		refresh(app.Context(), statusSvc, tray, window)
	})

	// Revealed once the app is actually running, not here. Showing a window
	// before Run silently does nothing for any window but the first — the
	// sessions window opened from the tray menu and never from --sessions,
	// which looked like a broken flag rather than a lifecycle rule.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// A transparent backdrop leaves the window itself clear, so it has to be
		// repainted or the gap before first paint shows the desktop instead of
		// white. Only reachable now: setting it in the options is undone by the
		// backdrop, which Wails applies afterwards.
		window.SetBackgroundColour(inkColour)
		sessionsWindow.SetBackgroundColour(inkColour)

		if *showWindow {
			reveal(window)
		}
		if *showSessions {
			reveal(sessionsWindow)
		}
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// quitting is set before the windows are torn down, so their close hooks stop
// cancelling and let the WebView2 instances go first. Everything else about the
// app's lifetime is "hide, never close" — the tray is the app — and this is the
// single exception.
var quitting atomic.Bool

// inkColour is --ink from the frontend's palette. It has to exist on the Go
// side too: the window is painted before there is a document to read CSS from.
var inkColour = application.NewRGB(0x0E, 0x0E, 0x12)

// newWindow builds the detail window. It starts hidden and hides rather than
// closes, so the tray outlives it — closing the window must not quit the app.
func newWindow(app *application.App) *application.WebviewWindow {
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "main",
		Title: AppName,
		// WKWebView is created opaque and paints its own background until the
		// document's first paint, and RGBA's zero value is fully transparent —
		// so an unset colour here means the window has nothing behind that
		// first frame. Matching --ink keeps the reveal dark end to end.
		BackgroundColour: application.NewRGB(0x0E, 0x0E, 0x12),

		Width:         720,
		Height:        560,
		Hidden:        true,
		DisableResize: false,
		URL:           "/",
		// Frameless on Windows only. macOS hides the title bar while keeping the
		// traffic lights (MacTitleBarHiddenInset), which has no Windows
		// equivalent — a native caption bar on a tray popover looks like a
		// dialog that wandered out of a settings screen. The header is already
		// draggable, and clicking away dismisses it, so the bar was carrying no
		// weight. The sessions window keeps its frame: that one is a real window
		// you keep open, and taking its minimise and close buttons away to match
		// would be fidelity for its own sake.
		Frameless: runtime.GOOS == "windows",
		// Windows' default is SystemDefault, so on a light-themed machine the
		// frame and WebView2's own pre-paint background come up light — which is
		// the white flash, arriving from the one surface CSS cannot reach. This
		// UI is dark whatever the system says, so declaring it is right on its
		// own merits and not only as a fix.
		Windows: application.WindowsWindow{
			Theme: application.Dark,
			// Wails' default mapping funnels five Windows events into the two
			// common focus ones, and two of them — WindowSetFocus and
			// WindowKillFocus — fire as focus shuttles between the host window
			// and WebView2's own child window. That shuttle looks exactly like
			// the user leaving and coming back, so it kept re-arming the reveal
			// grace and the click-away hide never ran.
			//
			// WindowInactive and WindowActive are the "this window is no longer
			// the active one" signals, which is what clicking away actually is.
			// Everything else is Wails' default, restated because supplying a
			// mapping replaces it wholesale rather than merging.
			EventMapping: map[events.WindowEventType]events.WindowEventType{
				events.Windows.WindowInactive:     events.Common.WindowLostFocus,
				events.Windows.WindowActive:       events.Common.WindowFocus,
				events.Windows.WindowClickActive:  events.Common.WindowFocus,
				events.Windows.WindowClosing:      events.Common.WindowClosing,
				events.Windows.WindowShow:         events.Common.WindowShow,
				events.Windows.WindowHide:         events.Common.WindowHide,
				events.Windows.WindowDidMove:      events.Common.WindowDidMove,
				events.Windows.WindowDidResize:    events.Common.WindowDidResize,
				events.Windows.WindowMinimise:     events.Common.WindowMinimise,
				events.Windows.WindowUnMinimise:   events.Common.WindowUnMinimise,
				events.Windows.WindowMaximise:     events.Common.WindowMaximise,
				events.Windows.WindowUnMaximise:   events.Common.WindowUnMaximise,
				events.Windows.WindowRestore:      events.Common.WindowRestore,
				events.Windows.WindowFullscreen:   events.Common.WindowFullscreen,
				events.Windows.WindowUnFullscreen: events.Common.WindowUnFullscreen,
				events.Windows.WindowDPIChanged:   events.Common.WindowDPIChanged,
			},
			// A tray popover is not a program you alt-tab to. macOS says the same
			// thing with ActivationPolicyAccessory and LSUIElement, which keep it
			// out of the Dock; this is the Windows half of that, and without it
			// the popup had a taskbar button showing a thumbnail of a window that
			// disappears the moment you click it.
			HiddenOnTaskbar: true,
		},
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
			// WKWebView is created opaque white and Wails never sets the
			// webview's own colour — webviewSetBackgroundColour exists in the
			// darwin bindings with no caller — so the window colour alone could
			// not stop the flash. Transparent makes the webview draw nothing,
			// leaving the window's colour visible until the document paints.
			//
			// This also resets the window to clearColor, which is why the
			// colour is re-applied on ApplicationStarted.
			Backdrop: application.MacBackdropTransparent,
		},
	})

	// RegisterHook, not OnWindowEvent: only the hook runs early enough to
	// cancel the close. OnWindowEvent fires once the window is already going.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		// Shutting down is the one time a close should be allowed through. Both
		// windows host a WebView2, and cancelling every close means both are
		// still alive when the process exits — Chromium then tries to unregister
		// its window class while windows still exist and reports
		// "Failed to unregister class Chrome_WidgetWin_0. Error = 1412"
		// (ERROR_CLASS_HAS_WINDOWS). Letting them close first is the ordering it
		// is asking for.
		if quitting.Load() {
			return
		}
		e.Cancel()
		w.Hide()
	})

	// This window hangs off the menu bar icon, so it behaves like a menu bar
	// popover: click anywhere else and it goes away. Without this it lingers on
	// screen like an ordinary window that merely happens to have no Dock icon.
	//
	// Guarded by a grace period after being revealed. Showing a window is not
	// instantaneous, and a stray resign-key during that window — the tray menu
	// closing, focus settling — would hide it before it was ever usable, which
	// reads as the menu item doing nothing.
	// Gaining focus restarts the grace period too. Clicking the tray icon shows
	// this window from inside Wails without going through reveal, so focus is
	// the only signal that path gives us.
	w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		markRevealed(w)
	})
	w.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		if time.Since(lastReveal(w)) < revealGrace {
			return
		}
		// A system dialog takes focus, so the folder picker was dismissing the
		// window that opened it — and with it the form the answer was going
		// into. Standing down while one is up is the only correct reading of
		// "the user clicked away": they did not, they answered a question this
		// window asked.
		if bridge.NativeDialogOpen() {
			markRevealed(w) // fresh grace for the focus bouncing back
			return
		}
		w.Hide()
	})
	return w
}

// revealGrace is how long after a reveal focus loss is ignored. Long enough to
// cover the window appearing and the tray menu dismissing itself, short enough
// that a genuine click elsewhere still dismisses it promptly.
const revealGrace = 700 * time.Millisecond

var revealed struct {
	mu sync.Mutex
	at map[*application.WebviewWindow]time.Time
}

func markRevealed(w *application.WebviewWindow) {
	revealed.mu.Lock()
	defer revealed.mu.Unlock()
	if revealed.at == nil {
		revealed.at = map[*application.WebviewWindow]time.Time{}
	}
	revealed.at[w] = time.Now()
}

func lastReveal(w *application.WebviewWindow) time.Time {
	revealed.mu.Lock()
	defer revealed.mu.Unlock()
	return revealed.at[w]
}

// newSessionsWindow builds the sessions manager: every session this machine can
// see, from the live ~/.claude, each Claude Desktop install and the synced repo.
//
// A window of its own rather than a tab in the status window. The status window
// answers "is my sync healthy" and is read in seconds from the tray; this one is
// a browser you keep open and scroll, wants far more width, and has nothing to
// do with sync health. Sharing a window would have meant one of them fitting
// badly.
func newSessionsWindow(app *application.App) *application.WebviewWindow {
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "sessions",
		Title: AppName + " — Sessions",
		// Same reason as the status window: see newWindow.
		BackgroundColour: application.NewRGB(0x0E, 0x0E, 0x12),
		Width:            1200,
		Height:           700,
		MinWidth:         900,
		Hidden:           true,
		URL:              "/sessions.html",
		// Dark like the popup, but this one KEEPS its taskbar button. It is a
		// real window you leave open and come back to, and on Windows that means
		// alt-tab and the taskbar — the same reason it keeps its native frame.
		// Hiding it to match the popover would be fidelity for its own sake.
		Windows: application.WindowsWindow{Theme: application.Dark},
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
			// Same as the status window: see newWindow.
			Backdrop: application.MacBackdropTransparent,
		},
	})
	// Hide rather than close, exactly as the status window does: the tray is
	// the app, and closing either window must not take it down.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		// Shutting down is the one time a close should be allowed through. Both
		// windows host a WebView2, and cancelling every close means both are
		// still alive when the process exits — Chromium then tries to unregister
		// its window class while windows still exist and reports
		// "Failed to unregister class Chrome_WidgetWin_0. Error = 1412"
		// (ERROR_CLASS_HAS_WINDOWS). Letting them close first is the ordering it
		// is asking for.
		if quitting.Load() {
			return
		}
		e.Cancel()
		w.Hide()
	})
	return w
}

// reveal shows the window and raises it. Show alone leaves it behind whatever
// has focus, which reads as "the menu item did nothing" — and as an accessory
// app there is no Dock icon to click as a fallback.
//
// reveal shows the window and puts it in front.
//
// Focus alone is not enough and no amount of retrying fixes it: raising by
// focus is a race against whoever else wants to be key, and when the reveal
// came from a click in another of our windows — the status window's Sessions
// button — that click finishes after we return and hands key straight back.
//
// So the window is lifted to the floating window LEVEL for a moment instead.
// Level ordering is not a race: a floating window sits above every normal one
// regardless of focus, so nothing that happens afterwards can bury it. Once it
// is up, it drops back to the normal level — keeping its place at the front,
// but no longer floating over unrelated apps.
//
// Show() is called exactly once. Calling it twice races window creation and
// takes the app down with "window not found".
func reveal(w *application.WebviewWindow) {
	markRevealed(w)
	w.Show()
	w.SetAlwaysOnTop(true)
	w.Focus()
	time.AfterFunc(400*time.Millisecond, func() {
		w.SetAlwaysOnTop(false)
		w.Focus()
	})
}

// newTray builds the menu bar icon. Clicking it toggles the window beneath the
// icon; the menu carries the actions.
func newTray(app *application.App, window, sessions *application.WebviewWindow, actions *bridge.Actions) *application.SystemTray {
	tray := app.SystemTray.New()

	menu := app.NewMenu()
	menu.Add("Open " + AppName).OnClick(func(*application.Context) { reveal(window) })
	menu.Add("Sessions…").OnClick(func(*application.Context) { reveal(sessions) })
	menu.AddSeparator()
	// Running an action from the tray opens the window too: the output streams
	// into the drawer, and a sync that reports a tripwire refusal with nobody
	// looking is the failure mode this whole project exists to end.
	menu.Add("Sync now").OnClick(func(*application.Context) { runFromTray(actions, window, bridge.ActionSync) })
	menu.Add("Pull").OnClick(func(*application.Context) { runFromTray(actions, window, bridge.ActionPull) })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		// Close the windows before quitting, and the sessions window before the
		// popup: WebView2 holds a window class per environment, and tearing the
		// process down around live ones is what makes Chromium complain on exit.
		// Close() is synchronous, so by app.Quit() they are gone.
		quitting.Store(true)
		sessions.Close()
		window.Close()
		app.Quit()
	})
	tray.SetMenu(menu)

	tray.AttachWindow(window).WindowOffset(5).WindowDebounce(200 * time.Millisecond)

	// Amber until the first poll answers — better an honest "unknown" than a
	// green icon we have not earned.
	//
	// HERE, before app.Run, on purpose. SystemTray.SetIcon stores the bytes
	// while the native tray does not exist yet and registration carries them in
	// with NIM_ADD; called later it goes straight to the native side, and if the
	// icon is not registered at that instant Windows logs "ShellNotifyIcon
	// NIM_MODIFY failed". Early is the quiet path, not the noisy one.
	applyLevel(tray, health.Amber)
	tray.SetTooltip(AppName + " — checking…")
	return tray
}

// trayReadyGrace is how long the poll waits before its first pass, so it cannot
// touch the tray icon before the platform has registered one.
const trayReadyGrace = 750 * time.Millisecond

// poll refreshes the tray from the engine on a fixed cadence. It stops with the
// app's context so quitting does not leave a goroutine mid-git.
func poll(app *application.App, svc *bridge.Status, tray *application.SystemTray, window *application.WebviewWindow) {
	ctx := app.Context()
	// The first pass sets the tray icon, and this goroutine starts before
	// app.Run. Reaching the native tray before Windows has registered it is
	// what logs "ShellNotifyIcon NIM_MODIFY failed"; the icon set above is
	// already showing, so there is nothing to race for.
	select {
	case <-ctx.Done():
		return
	case <-time.After(trayReadyGrace):
	}
	for {
		refresh(ctx, svc, tray, window)

		// Chosen after each pass rather than by a fixed ticker, so opening or
		// closing the window changes the rate from the next tick.
		wait := pollInterval
		if window != nil && window.IsVisible() {
			wait = pollOpen
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// refresh reads health once and pushes it to the icon, the tooltip, and any
// open window.
func refresh(ctx context.Context, svc *bridge.Status, tray *application.SystemTray, window *application.WebviewWindow) {
	rep, err := svc.Health(ctx)
	if err != nil {
		// A snapshot we cannot take is itself a state worth showing.
		applyLevel(tray, health.Amber)
		tray.SetTooltip(AppName + " — status unavailable")
		return
	}

	applyLevel(tray, rep.Level)
	tray.SetTooltip(rep.Tooltip(AppName))

	// Let an open window re-render without waiting for its own poll.
	if window != nil {
		window.EmitEvent("clauderig:health", rep)
	}
}

// runFromTray starts an action and shows the window so its output is visible.
func runFromTray(actions *bridge.Actions, window *application.WebviewWindow, a bridge.Action) {
	reveal(window)
	if err := actions.Run(context.Background(), string(a)); err != nil {
		// Busy or unresolvable binary — the window is already up, so the
		// frontend's own error row is the right place for it.
		window.EmitEvent(bridge.EventActionDone, bridge.Done{Action: string(a), Error: err.Error()})
	}
}

// applyLevel swaps the tray icon to lvl's colour. Both variants are set so the
// menu bar picks the right one when the system theme flips.
//
// Deliberately not a macOS template icon: template icons are black+transparent
// by definition, which would throw away the health colour that is the whole
// point of the icon. See ui/assets/README.md.
func applyLevel(tray *application.SystemTray, lvl health.Level) {
	light, dark := assets.Tray(lvl)
	tray.SetIcon(light)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		tray.SetDarkModeIcon(dark)
	}
}
