---
type: fix
---

rig prune now removes merged worktrees that contain submodules — git refuses a plain `worktree remove` on them ("working trees containing submodules cannot be moved or removed"), so prune force-removes worktrees it has already verified clean. The confirm screen also no longer swallows the per-item table when a run removes nothing, so a failed removal shows its reason instead of a bare "nothing to prune".