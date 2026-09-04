---
type: fix
"github.com/rigsmith/rigsmith"
---

rig: `stack pull` reports conflicts where they actually are, and settles the ones that were never upstream's to decide. The error named the prefix that was asked for, whichever paths had conflicted; it now lists the files. And a history filtered to one prefix cannot change anything outside it, so conflicts elsewhere — which mean the fetched history shares a full stackspace commit as an ancestor — are settled as the stackspace's own version, with a note saying how many, where, and why, instead of eighty conflicts in the wrong directory. Conflicts inside the prefix stay yours.
