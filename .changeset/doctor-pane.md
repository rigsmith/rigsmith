---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith"
---

The window has a Health pane: every `clauderig doctor` check, and a button on the ones it can repair.

The same `doctor.Run` the CLI calls, against the same environment — a second implementation would eventually disagree with the command line about whether a machine is healthy, and that is not a disagreement anyone can settle from the outside.

Only the checks that are saying something are shown, with a toggle for the passing ones. A list where the one line worth acting on sits under a dozen reading "ok" is a list nobody reads, which is exactly what happened to session filing when it was added as a check the CLI would print.

Fixes run by check id rather than by name, so rewording a label cannot change what the window is allowed to ask for. A check that can repair itself but has no id gets no button — an offer that could only fail is worse than no offer. Applying one returns a freshly run report, so the pane shows the state after the repair rather than the one that prompted it.

It checks **this machine**, not a repository. `clauderig doctor` is run from inside the repo you mean, so its working directory answers the question; the window has no such directory — it inherits whatever the launcher used, which is a shell's cwd from a terminal and the filesystem root from Finder. Reporting worktree discipline for a repository nobody chose, that changes depending on how the app was started, would be worse than not reporting it, so the pane says where that check lives instead. The global sync hooks stay: they are `~/.claude`, not any repo.

Deliberately not on the status poll. These checks shell out to git and gh, look for binaries on PATH and size the Desktop store; that is far too much to repeat every five seconds for an answer that only changes when something changes. It runs when the window opens, after a fix, and on **Re-check**.
