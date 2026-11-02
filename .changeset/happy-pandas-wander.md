---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

Include reachable sibling source signals in stack overlays and recognize hand-owned gated imports. Propagate signals through evaluated ProjectReferences so guarded tests run, and refuse partial upgrades of customized legacy imports. Fail clearly if an SDK item-contract change would silently drop ProjectReference source signals.
