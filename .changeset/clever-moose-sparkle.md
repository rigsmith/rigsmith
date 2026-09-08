---
type: fix
scope: rig
"github.com/rigsmith/rigsmith": patch
---

`rig stack` now reaches private upstreams and forks with your `gh` login, for pushes as well as fetches; re-imports a member you had removed; fails with a clear error instead of quietly recording an import that fetched nothing; and says in `status` and `pull` when a member's directory is missing instead of calling it up to date.
