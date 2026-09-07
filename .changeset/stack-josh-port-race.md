---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack` commands no longer fail with "josh-proxy exited before becoming
ready". The engine is handed a free port, and in the moment between rig letting
that port go and the engine binding it, something else can take it. Rig now
notices the port was taken and starts again on another one, rather than
reporting the command as failed.
