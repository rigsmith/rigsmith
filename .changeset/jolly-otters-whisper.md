---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

A failure in one publishing channel no longer withholds another.

The release publishes in sequence — GitHub release, then npm, then the winget submissions — and a step is skipped when an earlier one fails, whatever its own error handling says. So when 1.19.0's npm credential expired, all five winget packages were silently skipped: the release was out, the manifests were fine, and nothing was submitted or reported. The winget step now depends on the release having published, and on nothing after it, and the dry-run artifacts survive a later failure too — they are most wanted exactly when something went wrong.