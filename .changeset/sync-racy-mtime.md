---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig sync` no longer misses a transcript that was rewritten without its
timestamp moving. Sync decides a file is already backed up by comparing its
modification time to the copy it staged last time, and two writes close enough
together share a timestamp — so the second one was skipped, the backup kept the
earlier content, and nothing said so. How close is "close enough" depends on
the filesystem, so sync now measures that and stages the file again when a
timestamp is too near its own last run to be evidence. On the filesystems most
machines use the window is well under a millisecond and nothing changes.
