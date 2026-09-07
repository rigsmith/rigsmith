---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

rig stack: `propose --commits <range>` sends only the commits you name, so a second fix in the same project can be its own pull request instead of carrying the first one's changes with it. It needs `trackBranch` in the manifest and keeps that branch current with everything the prefix holds — a selected branch is only part of the divergence, and `rig stack init` rebuilds from it, so without that a rebuild elsewhere would quietly build without the fixes you left out.

`rig stack status` now reports how many commits a prefix diverges from upstream by: a plain `propose` sends all of them, and nothing said so.