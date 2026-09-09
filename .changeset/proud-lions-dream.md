---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The window now tells you when the machine-wide Claude Desktop is launched, because that is the window a session meant for a profile can end up in.

A watch scans the running Claude Desktop processes every ten seconds — five while the notice is up — and raises a small window the moment one appears with no `--user-data-dir`: the ordinary install, the one the Dock and Spotlight start. While it is open a `claude://` deep link is routed by scheme rather than to a chosen window, so **Open in Desktop** can land there instead of the profile that was picked, and `clauderig desktop send --session` refuses rather than guess. Until now the first sign of any of that was a refusal, or a conversation filed under the wrong account.

It is the app's own window rather than an OS notification, and deliberately: a real notification needs a signed bundle and the user's permission, so it would be silent in a dev build and silent again for anyone who ever declined the prompt. A window is the one surface a tray app can always put on screen.

Three decisions about when NOT to speak, each of them a way this could have become noise. A Desktop window already open when the tray starts is not a launch, so starting the app never greets you with a warning about something you have had open all morning. A failed process scan holds the previous answer instead of reading as "closed", which would otherwise make the next successful scan look like a launch. And a machine with no clauderig Desktop profiles is never warned at all — with nothing to route a session to, the main app is simply Claude Desktop.

Raising only on the transition is also what makes dismissing it work: it will not come back until that app is closed and opened again. **Don't warn again** on the notice turns it off for good, and the tray's **Warn when Claude Desktop opens** is the way back on — an off switch whose on switch is a file somebody has to find is not a setting.