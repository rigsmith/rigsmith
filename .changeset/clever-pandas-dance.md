---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`changerig status` now gates the way `changeset status` does in @changesets v3. It fails when a package that would version changed since `--since`, or the base branch by default, and there is no changeset, which is now checked even without `--since`. With nothing pending and nothing changed it exits 0 instead of 1, and `--output` writes an empty plan, so a script can tell "nothing to release" from an error. Ignored packages, and private ones that are not versioned, no longer trip the gate.