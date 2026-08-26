---
type: fix
"github.com/rigsmith/rigsmith"
---

project discovery no longer walks into a linked git worktree. A worktree is a second checkout of the same repository, so discovering it returned a duplicate of every manifest — and a release could then act on the copy, writing its changelog into the worktree or building a tag out of the worktree's path. Submodules and nested clones are different repositories you put there deliberately, and stay discoverable; `rig --include-worktrees` still opts worktrees back in, and now reaches the walker rather than only the filter after it. The same rule applies to Node workspace globs and `rig copy`, which do their own traversal.
