---
type: fix
scope: rig
"github.com/rigsmith/rigsmith": patch
---

`rig stack` now reaches private upstreams with your `gh` login, re-imports a member you had removed, and fails with a clear error instead of quietly recording an import that fetched nothing.
