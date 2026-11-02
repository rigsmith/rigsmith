---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

Recognize squash/rebase-merged owned stack members by upstream tree content. Preserve individual new commits on a safe upstream base, refuse ambiguous replay, verify ancestry before every member push, and exclude remove/add administrative history.
