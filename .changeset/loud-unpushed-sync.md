---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `status` and `doctor` now report whether anything actually reached the remote. `last sync` is the last local COMMIT, which keeps advancing happily while every push is rejected — so a machine whose remote had diverged showed "✓ last sync 2 minutes ago" and "✓ remote reachable" through ten days of backing nothing up. `status` gains an `unpushed` line and `doctor` a `pushed` check that FAILS (not warns) when commits have never left the machine, naming the reconcile path when the remote has also moved on. Both read the remote-tracking ref, so neither touches the network; `behind` is therefore a lower bound, while `ahead` — the number that matters — is exact. New `gitrepo.AheadBehind` reports 0/0 rather than an error when there is no tracking ref, so "cannot tell" never renders as lost work.
