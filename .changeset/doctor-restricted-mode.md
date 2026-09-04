---
type: feat
"github.com/rigsmith/rigsmith"
---

clauderig: `doctor` warns when `CLAUDE_CODE_RESTRICTED` is set. Claude Code's restricted mode ignores every settings.json tier, so the sync hooks and the guard hook installed there never fire in such a session — previously nothing said so.
