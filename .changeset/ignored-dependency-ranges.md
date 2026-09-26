---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Ignoring a package now fully holds it back: its changesets no longer change the version ranges other packages use to depend on it. The same goes for packages that `version --only` leaves out.
