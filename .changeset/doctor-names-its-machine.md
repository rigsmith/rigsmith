---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig doctor` now checks this machine under the name it actually has.

It built its environment with the placeholder identity — the literal name `this`, which clauderig uses for a host whose name cannot be resolved. That name is load-bearing history here: a machine that failed to resolve once registered a ghost device under it that sat in the synced registry from June to August 2026 before being removed by hand.

Nothing writes from the doctor, so it never registered anything. But the machine it was given decides which root locations resolve, so a machine with a per-machine root override keyed by its real name had that override ignored and was told its paths resolve — when they resolve to somewhere else. Measured on a machine with no overrides the two agree exactly, which is why this went unnoticed.
