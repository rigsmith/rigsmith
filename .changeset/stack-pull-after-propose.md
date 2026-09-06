---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack pull` no longer refuses forever once a member's work has been proposed.

With work on a fork branch and upstream moving on, pull merged the two correctly and then reported failure anyway: `libfoo/ holds changes of its own, and moving it to <commit> needs the directory replaced`. Retrying never helped, and the advice it gave — send them first — could not be followed, because the work had already been sent.

The check exists for repinning a member to an older commit, where only replacing the directory can move it and replacing would discard. But a prefix also differs from upstream when a merge has just combined the two, which is the ordinary case, and the trees alone cannot tell those apart. It now looks at whether the merge moved anything: if it did, the work is done and there is nothing to replace. The protection against discarding unsent work is untouched and still guards the only step that discards.

Two more, found behind it:

`rig stack propose` recorded the branch it pushed to in `rig.stack.jsonc` and left the file uncommitted. `rig stack seed` then refused — a seed has to be a revision that exists — and a seed taken anyway carried the previous branch name, so rebuilding reached for work that was not there. Propose commits that record now, and only that file.

A stackspace rebuilt by `rig stack init` from a member's proposed branch did not know its content was already on the fork, so the next propose pushed an identical commit. It records what it rebuilt from, and propose treats content already on the branch as nothing to send — after asking the fork about the branch it is actually proposing to, so neither a renamed branch nor one deleted since is mistaken for work that is safely elsewhere.

Propose leaves a manifest you have edited yourself alone: the branch is still recorded, but committing it would put your unrelated change into a commit whose message describes something else.
