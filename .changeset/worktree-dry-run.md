---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig worktree new --dry-run` created the worktree.

So did `rig worktree rm --dry-run` remove one. Both do their work in process
rather than by shelling out, so neither passed through the place where
`--dry-run` is otherwise honoured, and the flag was accepted and ignored.

They now say what they would do and do nothing — not even creating the parent
directory, since an empty `<repo>-worktrees` left behind is still not "nothing
happened".
