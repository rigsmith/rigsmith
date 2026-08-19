---
"github.com/rigsmith/rigsmith": patch
---

clauderig: prune Desktop session sidecars whose transcript is gone. Transcripts were aged out of staging by mtime while their `claude-code-sessions` sidecars were never pruned at all, so the two trees drifted apart and staging accumulated titles for sessions that no longer existed. A staged sidecar is now retained exactly as long as the transcript it names, so retention drives both on one clock. Retention is deliberately NOT applied to a sidecar's own mtime — Desktop rewrites that on its own schedule, and sidecars a month old routinely name transcripts written days ago.
