---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The journal no longer sums two different retention numbers into one.

`aged out` was `RetentionPruned + RetentionByAge`. The first is a deletion — staged files removed because they passed the window — and is an event, normally zero. The second is a file in the live tree that the sync declined to stage because it is already too old. clauderig never deletes from `~/.claude`, so those files stay where they are and are declined again on every run: the count is a standing property of your tree, not something that happened. Added together, the constant swamped the event, and a sync that genuinely pruned something looked identical to the thousands that didn't.

They are now separate. `agedOut` is the prune, and is what the activity feed reports. `tooOld` is recorded but kept out of the summary line, for the same reason the unchanged-file count is: it would repeat forever without ever meaning anything new.

`clauderig sync` renames the per-root figure from `N aged out` to `N too old`, alongside the existing `N too large`. Nothing is aged *out* at that point — the file is simply left where it is. The separate `retention N aged file(s) pruned from staging` line was already the real deletion and is unchanged.
