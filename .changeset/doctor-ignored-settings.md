---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---
`doctor` reports settings Claude Code silently ignores at project or local scope. Since the 2026-09-02 release Claude Code ignores `permissions.defaultMode: "bypassPermissions"` in `.claude/settings.json` and `.claude/settings.local.json` (as it always has with `"auto"`), honouring it only from user or managed settings — so a repo that committed the value when it worked now gets a warning naming the file, instead of a quiet no-op. Nothing is changed in the file.
