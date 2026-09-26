---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`"versioning": { "bumpMinorPreMajor": true }` releases a major bump on a package below 1.0.0 as a minor (0.3.0 → 0.4.0), as release-please's `bump-minor-pre-major` does, and `status` reports it as a minor. Packages at 1.0.0 or above are unaffected, and 1.0.0 is reached with an explicit `--release-as`. It's off by default, as canon @changesets takes a major on 0.x to 1.0.0.
