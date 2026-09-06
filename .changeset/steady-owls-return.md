---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Give each sync-lock acquisition a distinct ownership token even when the clock returns the same timestamp, so a previous holder cannot release its replacement. Preserve compatibility with older PID/timestamp lock files.
