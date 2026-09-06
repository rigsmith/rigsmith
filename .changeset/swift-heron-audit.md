---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Stop re-reading the whole backup on every sync.

The pre-publication audit read all of staging on every run, whatever had
changed — nearly four minutes here to conclude that three files had moved, and
twice per publish. It now reads with a worker per core, and remembers which
files it found clean by size and mtime, so a run reads only what has moved
since. On a 1.8 GB tree: 3m52s to 27s cold, and 75ms warm.

Nothing is taken on trust that has not been read at exactly that size and
mtime. Findings are never cached — they are reported again on every run until
they are dealt with — a run that found something leaves no verdicts behind at
all, a packed transcript is always read through its parts, and the cache is
discarded whole when the scanner learns a new credential shape.
