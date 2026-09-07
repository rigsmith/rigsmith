---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig sync` no longer misses a transcript that was rewritten without its
timestamp moving. Sync decides a file is already backed up by comparing its
modification time to the copy it staged last time, which stops being reliable
on a filesystem whose clock ticks in whole seconds: two writes in the same
second share a timestamp, and the second one was skipped — the backup kept the
earlier content and nothing said so. Sync now notices when a timestamp is too
close to its own last run to be evidence, and stages the file again. On a
filesystem with sub-second timestamps, which is nearly all of them, nothing
changes.
