---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Clicking the menu bar icon now opens the Claude Desktop profiles, not the sync status.

A small popover lists every profile — account monogram, name, email, whether it is open — over one line of sync health with a button through to the full status window. Status is still there, one click further in, and named in the right-click menu.

The reason is what each is for. Sync status is something you check, occasionally, usually because something told you to. Which Claude Desktop window to go to is something you do many times a day, and the OS is no help with it: every instance is one application as far as macOS is concerned, so the Dock shows identical tiles and the app switcher one entry. The thing this tray can do that nothing else can is now the thing it does first.

One click per row, and the row's state decides what it does: an open profile is raised by pid in-process — the same call the tray menu's window list makes — and a closed one is opened through `clauderig desktop open`, which owns launching a profile properly. The machine-wide app sits last, dimmed, below a rule: it is not a profile, no account is bound to it, and a session opened there lands under whichever login that install happens to hold.

The popover sizes itself to the number of profiles found rather than being a fixed panel with empty space below two rows.

The right-click menu gains the three screens the popover is not: Status, Sessions — list, and Sessions — places. That window was always two screens wearing one label, and the menu now opens either directly.