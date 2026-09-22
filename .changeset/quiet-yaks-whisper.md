---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig release` skips the `version` step when nothing is pending, instead of failing on it, so a release that only publishes what is already versioned still runs. Private packages versioned with `privatePackages.version` get no git tag or forge release unless `privatePackages.tag` is also set.