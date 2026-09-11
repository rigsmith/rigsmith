---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig desktop main` opens, or brings forward, the ordinary Claude Desktop — the one with no profile behind it.

Once a profile window is open, that app is unreachable from the OS: every instance is one application as far as macOS is concerned, so the Dock icon, Spotlight and `open -a` all activate whichever instance is already running, which is the profile. There was no gesture anywhere that meant "the other one", and clauderig had no verb for it either — everything it launches, it launches against a profile.

It scans before acting, because the two cases need opposite things. Nothing running: a new instance with no profile flag, which is what leaves it on the app's own data directory. Already running: that process is raised by pid, since asking the OS to activate the application is the problem restated. Launching over a running instance would give two machine-wide windows sharing one history, so a scan that fails refuses rather than guessing.

Every path that leaves a window on screen prints the same warning, because the window is indistinguishable from a profile's on screen (the paths that return early have no window to warn about): it is not a clauderig profile. No account is bound to it, `open`, `quit` and `send` cannot name it, and it competes for `claude://` deep links. Its history is still backed up — sync walks it as the `desktop` root, the same as every profile — but whose sessions those are is whatever that install happens to be signed into.

On macOS, raising a named window goes through System Events, so the first run asks for Automation permission and a refusal is reported rather than silently doing nothing. On Windows there is no equivalent worth the dependency: it says the window is open and names the process instead.