---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack send` refuses when upstream has moved past your last `stack pull`. The workspace holds a snapshot taken at that pull, so committing it onto a newer tip would open a pull request that reverts whatever landed in between. Pull first, then send.
