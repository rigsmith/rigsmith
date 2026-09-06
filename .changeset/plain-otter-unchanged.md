---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Stop scanning unchanged staged files twice per sync.

The unchanged path re-read and re-scanned every staged file on every run, then
the audit read the whole tree again — two full passes over gigabytes to
conclude that three files had moved. It now consults the verdicts the audit
already reached, and reads anything it cannot account for.

Read-only, and narrow: the audit writes those verdicts, and only when it finds
nothing anywhere in the tree. A file staged by an older clauderig, which no
audit ever vouched for, still fails the sync until it is dealt with.
