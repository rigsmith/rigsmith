---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---
the overlay `stack wire` writes now sets `ProduceReferenceAssembly=false`, so swapping a package for a project reference no longer breaks anything that reads a dependency's internals through a publicizer. A project reference hands consumers a reference assembly the publicizer never rewrote, and every internal came back as `CS0122` on members that had not changed. `stack doctor` reports a rig-written overlay that predates this as out of date, an overlay that was never written as missing, and one left over after the last cross-member reference went as such, rather than calling an unwired stackspace healthy; the Go adapter reports a stale generated `go.work` the same way.
