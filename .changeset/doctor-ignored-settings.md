---
type: feat
"github.com/rigsmith/rigsmith"
---

clauderig: `doctor` reports settings Claude Code silently ignores at project or local scope. `defaultMode: "bypassPermissions"` is now dropped from `.claude/settings.json` and `.claude/settings.local.json` (as `"auto"` always was), honoured only from user or managed settings — so a repo relying on sync to carry it across machines gets a warning instead of a quiet no-op.
