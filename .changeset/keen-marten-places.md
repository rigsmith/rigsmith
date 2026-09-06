---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Add a Places mode to the sessions window, for finding a session by where it was
kept rather than by what was in it.

The list answers "which session was that". Places answers "where was I" — it
walks stores instead of sessions, so you can open a Desktop, a profile, or a
project and see what is filed there. It shows the things a session list cannot:
a Desktop sidecar for a session whose conversation lives in the CLI tree (with
the link to that transcript), the same store live and synced side by side, and
the config each profile carries beyond its sessions.
