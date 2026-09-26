---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With commit-based versioning, each package now counts commits from its last release tag in the format you tag with: `name@version`, `v1.2.0`, or your `tagTemplate`. Tags like `lib@1.0.0` weren't found before, so every release counted the package's whole history again.
