---
"github.com/rigsmith/rigsmith": minor
---

Add `clauderig mv <src> <dst>` — move or rename a directory and relink its Claude Code history so the conversation stays attached. It renames the `~/.claude/projects` slug dir(s), rebases the cwd inside the transcripts, and updates the Desktop session metadata and settings additionalDirectories. Guards against moving a directory a live Claude session is running in, and against clobbering existing destination history. `--dry-run` previews; the move requires an interactive confirmation.
