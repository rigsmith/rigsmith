---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The worktree guard now catches agents that isolate themselves.

A subagent launched with `isolation: "worktree"` creates the same
`.claude/worktrees/<name>` checkout the guard refuses `EnterWorktree` for, and
went straight past it — one was created in a guarded repo. Both routes are
closed now, including `git worktree add` aimed at that directory. Removing one
is still allowed, since that is the way out.

`clauderig doctor` also reports worktrees it finds under `.claude/worktrees`.
They are invisible to `rig worktree list` and never reaped by `rig prune`, so
they accumulate unseen — one repo had 28 registered before anyone looked.
