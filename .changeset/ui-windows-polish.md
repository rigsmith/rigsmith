---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Four things the first Windows run turned up.

**The font stack named only Apple's faces.** `ui-monospace` and `SFMono-Regular` mean nothing on Windows or Linux, so both fell through to the generic `monospace` — whatever the engine felt like. It now names Cascadia (ships with Windows Terminal), Consolas (ships with Windows itself) and DejaVu / Liberation for Linux, so every platform gets a face that was actually chosen.

**The tray icon was squashed.** The icons are 44px, which is the macOS retina menu bar slot (22pt @2x). Windows draws the notification area icon at 16, or 32 on a high-DPI display, and scales whatever it is handed — from 44 that is a non-integer ratio, and the mark came out visibly squeezed. There is now a 32px set for Windows. The README used to claim 44 "downsamples cleanly" on Windows; a screenshot from a real machine says otherwise, and it has been corrected rather than left to mislead the next person.

**`ShellNotifyIcon NIM_MODIFY failed (icon not registered)`, twice per launch.** The tray icon was being set while the tray was still being built, and on Windows the notification area icon does not exist until the app runs. It is set on `ApplicationStarted` now, where the tray definitely exists. macOS never minded, but there was no reason to do it early there either — the first poll sets the real level within seconds.

**A native caption bar on a tray popover.** macOS hides the title bar while keeping the traffic lights, which has no Windows equivalent, so the popup arrived looking like a dialog that had wandered out of a settings screen. It is frameless on Windows now. The header was already draggable and clicking away already dismisses it, so the bar was carrying no weight — but "dismissible by clicking away" is not discoverable, so Windows gets an explicit close button, hidden on macOS and Linux where the window frame provides one. The header's 38px top padding exists to clear the traffic lights and is dropped on the platforms that have nothing overlapping.

The sessions window keeps its native frame on Windows. That one is a real window you keep open, and taking its minimise and close buttons away to match the popover would be fidelity for its own sake.
