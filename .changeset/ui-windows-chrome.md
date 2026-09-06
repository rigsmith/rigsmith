---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Three more from running the window on Windows.

**No taskbar button for the tray popover.** A popover is not a program you alt-tab to, and it had a taskbar button showing a thumbnail of a window that vanishes the moment you click away from it. macOS says this with `ActivationPolicyAccessory` and `LSUIElement`, which keep it out of the Dock; `HiddenOnTaskbar` is the Windows half of the same statement. The sessions window keeps its button — it is a real window you leave open and come back to, which on Windows means the taskbar and alt-tab, the same reason it keeps its native frame.

**The white flash.** Unlike macOS, Wails already handles this properly on Windows: it paints `WM_ERASEBKGND` with the configured colour and sets WebView2's background at creation, so the window was never the problem. `WindowsWindow.Theme` defaults to `SystemDefault`, though, so on a light-themed machine the frame and WebView2's pre-paint background come up light — the one surface CSS cannot reach. Both windows declare `Theme: Dark` now, which is right on its own merits since this UI is dark whatever the system says.

**`Failed to unregister class Chrome_WidgetWin_0. Error = 1412` on exit.** That is `ERROR_CLASS_HAS_WINDOWS`: Chromium unregistering its window class while windows still exist. Both windows host a WebView2, and both cancel every close and hide instead — the tray is the app, so closing a window must never quit it. The consequence was that both were still alive when the process exited. Quitting now sets a flag that lets the close hooks through, closes both windows, and only then quits.

The tray icons are also rendered at 64px rather than 32. `SM_CXSMICON` is 16, 20, 24 or 32 depending on the display's scaling, and Wails scales one image to whatever that is — 64 divides evenly into 16 and 32 and resamples gracefully to the awkward sizes a 125% or 150% display asks for.
