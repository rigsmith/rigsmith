---
type: fix
"github.com/rigsmith/rigsmith"
---

`clauderig sync` no longer ages project memories out of the sync. Memory files
(`~/.claude/projects/<slug>/memory/`) live under `projects/`, so the 30-day
retention window applied to them exactly as it did to transcripts — a memory that
hadn't been rewritten in a month stopped syncing and was then deleted from the
staged tree, leaving a restored machine with a `MEMORY.md` index pointing at files
it never received. Memory is durable state rather than a dated record, and a few KB
per file, so it is now exempt from the window on both paths that enforce it: the
copy-time cutoff and the staging prune. A project whose transcripts have all aged
out also keeps its slug when it still has memory, so the memory keeps a home.
