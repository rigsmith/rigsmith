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

// pollInterval is the tray's refresh cadence. status.Gather is local-only, so
// this costs a few git plumbing calls and no network.
const pollInterval = 45 * time.Second

func main() {
	// Linux tray support is desktop-environment dependent — GNOME needs an
	// AppIndicator extension for the icon to appear at all. --window is the
	// escape hatch when the tray never shows up.
	showWindow := flag.Bool("window", false, "open the window at startup instead of starting in the tray only")
	flag.Parse()

	statusSvc := bridge.NewStatus()

	app := application.New(application.Options{
		Name:        "clauderig",
		Description: "Claude Code sync status",
		LogLevel:    slog.LevelWarn,
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(frontend),
		},
		Services: []application.Service{
			application.NewService(statusSvc),
			application.NewService(bridge.NewActivity()),
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
	tray := newTray(app, window)

	go poll(app, statusSvc, tray, window)

	if *showWindow {
		reveal(window)
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
		Title:         "clauderig",
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

// reveal shows the window and raises it. Show alone leaves it behind whatever
// has focus, which reads as "the menu item did nothing" — and as an accessory
// app there is no Dock icon to click as a fallback.
func reveal(w *application.WebviewWindow) {
	w.Show()
	w.Focus()
}

// newTray builds the menu bar icon. Clicking it toggles the window beneath the
// icon; the menu carries the actions.
func newTray(app *application.App, window *application.WebviewWindow) *application.SystemTray {
	tray := app.SystemTray.New()

	menu := app.NewMenu()
	menu.Add("Open clauderig").OnClick(func(*application.Context) { reveal(window) })
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	tray.SetMenu(menu)

	tray.AttachWindow(window).WindowOffset(5).WindowDebounce(200 * time.Millisecond)

	// Amber until the first poll answers — better an honest "unknown" than a
	// green icon we have not earned.
	applyLevel(tray, health.Amber)
	tray.SetTooltip("clauderig — checking…")
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
		tray.SetTooltip("clauderig — status unavailable")
		return
	}

	applyLevel(tray, rep.Level)
	tray.SetTooltip(rep.Tooltip(""))

	// Let an open window re-render without waiting for its own poll.
	if window != nil {
		window.EmitEvent("clauderig:health", rep)
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
