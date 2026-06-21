---
"github.com/rigsmith/rigsmith": minor
---

`rig prune` now always shows why each worktree/branch was kept (the aligned name/state/reason table renders even when nothing is removable), and can force-remove kept items: `rig prune <name> --force` overrides a soft skip (unmerged, dirty, upstream-gone), and the confirm screen's `[f]` opens a checklist of forceable items. Hard rails still hold — the current, base, and primary checkouts can never be force-removed.
