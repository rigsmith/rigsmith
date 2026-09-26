---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

New option `"versioning": { "bumpMinorPreMajor": true }`: a breaking change to a package below 1.0.0 releases as a minor (0.3.0 → 0.4.0) instead of jumping to 1.0.0. In the changelog it's listed under Minor Changes, or under 💥 Breaking Changes if it's a typed breaking change such as `feat!`. It's off by default. When you're ready for 1.0.0, use `--release-as`.
