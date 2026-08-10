---
type: fix
"github.com/rigsmith/rigsmith"
---

A contended identity-journal lock no longer fails on Windows — it waits its turn, as it always did on Unix.

`clauderig`'s journal lock is an advisory file lock taken by exclusive create. Unix reports `EEXIST` when someone else holds it, which the retry loop expects. Windows reports `ERROR_ACCESS_DENIED` instead while the name is held by a file *pending deletion* — precisely the window another writer's release opens when it removes the lock. That isn't `EEXIST`, so it fell through to the fatal branch: two `clauderig` processes running at once could report `lock journal: Access is denied` rather than serializing, and the concurrent-writer test failed on roughly half of CI's Windows runs — including on commits whose diff was nothing but markdown.

The retry loop now treats that spelling as "someone has this name right now" and keeps waiting. It stays Windows-only, so a genuine permission fault on Unix is still fatal immediately; and even on Windows it is safe, because the loop already gives up at its deadline and proceeds unguarded rather than blocking a diagnostic forever.
