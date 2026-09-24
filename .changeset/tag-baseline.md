---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Commit-sourced releases find a package's last release tag the way the tag step names it (`name@version`, a Go module's `dir/vX.Y.Z`, a single app's `vX.Y.Z`, or the `tagTemplate`), taking the highest version. Only module-style tags were found before, so a package tagged `lib@1.0.0` counted its whole history again on every release.
