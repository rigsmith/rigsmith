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

// pollInterval is the tray's refresh cadence. status.Gather is local-only, so
// this costs a few git plumbing calls and no network.
const pollInterval = 45 * time.Second

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
	windowsSvc.Register("sessions", func() { reveal(sessionsWindow) })
	windowsSvc.Register("main", func() { reveal(window) })

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
	if *showWindow || *showSessions {
		app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			if *showWindow {
				reveal(window)
			}
			if *showSessions {
				reveal(sessionsWindow)
			}
		})
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// newWindow builds the detail window. It starts hidden and hides rather than
// closes, so the tray outlives it — closing the window must not quit the app.
func newWindow(app *application.App) *application.WebviewWindow {
	w := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "main",
		Title:         AppName,
		Width:         720,
		Height:        560,
		Hidden:        true,
		DisableResize: false,
		URL:           "/",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})

	// RegisterHook, not OnWindowEvent: only the hook runs early enough to
	// cancel the close. OnWindowEvent fires once the window is already going.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		w.Hide()
	})
	return w
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
		Name:     "sessions",
		Title:    AppName + " — Sessions",
		Width:    1200,
		Height:   700,
		MinWidth: 900,
		Hidden:   true,
		URL:      "/sessions.html",
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
		},
	})
	// Hide rather than close, exactly as the status window does: the tray is
	// the app, and closing either window must not take it down.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		w.Hide()
	})
	return w
}

// reveal shows the window and raises it. Show alone leaves it behind whatever
// has focus, which reads as "the menu item did nothing" — and as an accessory
// app there is no Dock icon to click as a fallback.
//
// Focusing once is not enough, for two separate reasons.
//
// A window that has never been shown has no platform window behind it yet:
// Show() creates one and returns early, so the first Focus() can land before
// there is anything to raise. And when the reveal came from a click in ANOTHER
// of our windows — the status window's Sessions button — that click finishes
// after we return, and its window takes key back, leaving the new one behind it.
//
// So focus is re-asserted a couple of times over the next half second, which
// covers both. Calling Show() twice does NOT work — the second races window
// creation and takes the app down with "window not found".
func reveal(w *application.WebviewWindow) {
	w.Show()
	w.Focus()
	for _, d := range []time.Duration{150 * time.Millisecond, 450 * time.Millisecond} {
		time.AfterFunc(d, w.Focus)
	}
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
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	tray.AttachWindow(window).WindowOffset(5).WindowDebounce(200 * time.Millisecond)

	// Amber until the first poll answers — better an honest "unknown" than a
	// green icon we have not earned.
	applyLevel(tray, health.Amber)
	tray.SetTooltip(AppName + " — checking…")
	return tray
}

// poll refreshes the tray from the engine on a fixed cadence. It stops with the
// app's context so quitting does not leave a goroutine mid-git.
func poll(app *application.App, svc *bridge.Status, tray *application.SystemTray, window *application.WebviewWindow) {
	ctx := app.Context()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	for {
		refresh(ctx, svc, tray, window)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
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
