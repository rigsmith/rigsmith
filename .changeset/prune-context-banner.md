---
"github.com/rigsmith/rigsmith": patch
---

`rig prune` now opens with a one-line context banner — the current working directory, the branch checked out there, and whether it's the repo's primary checkout or a linked worktree — so it's obvious which repo/branch you're tidying before anything is removed (and that this checkout is the one prune always protects).
