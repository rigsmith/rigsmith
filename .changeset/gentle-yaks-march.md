---
type: fix
"github.com/rigsmith/rigsmith"
---

project discovery no longer walks into a nested repository — a clone, a submodule, or a linked git worktree inside your tree. Their contents belong to that repository, and walking in found a second copy of every manifest: a release could write its changelog into a worktree instead of the repo, or build a tag name out of the worktree's path. A linked worktree keeps its `.git` as a *file*, which is why skipping the name alone never caught them.