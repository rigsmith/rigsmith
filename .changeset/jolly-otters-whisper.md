---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

A failure in one publishing channel no longer withholds another.

A release publishes in sequence — the GitHub release, then npm, then the winget submissions — and a step is skipped when an earlier one fails, whatever its own error handling says. So when 1.19.0's npm credential expired, all five winget packages were silently skipped: the release was out, the manifests needed no changes, and nothing was submitted or reported. The claudeRig UI release had the same shape, where a failed Homebrew cask push would take its winget submission with it.

Both now depend on the only thing a winget submission needs — a published release with archives to point at — checked directly rather than inferred from whether the build step as a whole succeeded, so a release that published its assets and then failed on a tap push still reaches winget. The dry-run artifacts survive a later failure too; they are most wanted exactly when something went wrong.
