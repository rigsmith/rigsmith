---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Every `clauderig doctor` check has a stable id, and its fix can be asked for by name.

`Result.Fix` is a `func(context.Context) error`. That works for a terminal and nowhere else: a function cannot cross a process boundary, so the repairs `doctor --fix` offers have been reachable only from `doctor --fix`. An id can cross anything.

`doctor.Find(ctx, env, id)` returns one check; `doctor.Fix(ctx, env, id)` runs its repair. Ids are separate from names because a name is display text — rewording "global sync hooks" must not change what a caller is allowed to ask for.

`Fix` **re-runs the checks** rather than accepting a `Result`. A fix is a closure over the state its check found, and state read minutes ago in another process is exactly the state you must not repair against. Re-running also means a problem resolved in between reports as resolved instead of being repaired a second time.

The two refusals are distinguishable: `ErrNoSuchCheck` and `ErrNotFixable` send a caller in different directions, and collapsing them into one "no" would leave a UI unable to tell "that button should not exist" from "that button should be disabled".

A test asserts every check has an id and that no two share one. A check added later without one would otherwise be invisible to anything but the terminal — silently absent rather than visibly broken.

Nothing about `clauderig doctor` changes for someone using it.
