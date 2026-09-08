---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack pull` and the import no longer read FETCH_HEAD: each fetch lands on a ref named for that call alone, so two rig processes in one stackspace can no longer merge each other's fetch while recording their own as the cursor.
