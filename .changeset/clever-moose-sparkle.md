---
type: fix
scope: rig
"github.com/rigsmith/rigsmith": patch
---

`rig stack` now reaches private upstreams with your `gh` login, and an import that fetches nothing fails with a clear error instead of quietly recording it as done.
