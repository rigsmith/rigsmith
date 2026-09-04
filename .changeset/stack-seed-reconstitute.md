---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---
a stackspace can be rebuilt on another machine from a few kilobytes. `stack seed <dir>` exports the root files — everything outside every prefix, manifest and cursors included — as a fresh one-commit repo; `stack init` on a clone of it reconstitutes each member at the commit its cursor records. A fork member comes back with its proposed-but-unmerged work: `init` imports from the branch it was last proposed to while that branch still exists, and a new `trackBranch` key names a fork branch to import from explicitly. Pull requests still go to upstream, and the cursor records the upstream commit the branch is based on.
