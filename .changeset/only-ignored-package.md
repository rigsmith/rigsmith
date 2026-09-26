---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`version --only` naming a package the config's `ignore` leaves out is an error that says so, as an unknown name is, and naming a private package that isn't versioned says it's private and points at `privatePackages.version`. Before, it printed "Nothing to version." and exited 0, which a release job would take for success.
