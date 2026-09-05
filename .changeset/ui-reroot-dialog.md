---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Re-filing a session is a button in the window, beside Open and Delete.

It asks for the directory and nothing else — where a conversation belongs is a judgement only the person who was there can make — and always dry-runs first, so the confirmation names a real number before a file is written.

The window's dialog offers both a text field and a **Browse…** button onto the system folder picker. Typing is faster when you know the path and can paste it; the picker is the only way to be certain the directory exists, and a typo here does not fail — it files the session somewhere plausible and wrong. The picker opens at the session's current directory, walking up if that folder has since been deleted, so the common case of moving a session one level down opens next to the answer.

The picker is attached to the window, so macOS runs it as a sheet rather than a free-floating panel. Unattached it opened *behind* the window that asked for it — the tray window is raised on reveal and nothing put the dialog above it. A sheet is always in front of its parent and moves with it, which also makes it obvious which question is being answered.

The tray window's auto-hide stands down while a system dialog is up. It hides on focus loss, which is what a menu bar window should do — but a native dialog takes focus, so opening the picker dismissed the window that asked for it and the form the answer was going into. The window consults a shared flag rather than the picker reaching in to disable the behaviour, so any dialog added later is covered by construction.
