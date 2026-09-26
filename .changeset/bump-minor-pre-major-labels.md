---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With `bumpMinorPreMajor`, `version --release-as <pkg>=1.0.0` labels the release a major in the plan and the changelog, as it is without the option, rather than `minor  zero  0.3.0 → 1.0.0`. The 0.x release a major change makes as a minor now lists it under `### Minor Changes`, as `status` reports it, instead of `### Major Changes`; a typed breaking change keeps its 💥 Breaking Changes heading.
