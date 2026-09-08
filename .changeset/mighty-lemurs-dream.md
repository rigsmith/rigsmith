---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack pull` now accepts a conflicted pull you resolved and committed by hand: the re-run records the cursor instead of refusing that the prefix "holds changes of its own".