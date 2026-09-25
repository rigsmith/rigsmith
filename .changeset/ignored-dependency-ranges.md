---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

An ignored package's changeset no longer rewrites its dependents' ranges. Its release was planned and dropped only afterwards, so a dependent (also held back) had its range moved to a version that was never released: with `ignore: ["lib", "app"]` and a major changeset on `lib`, `app`'s `"lib": "^1.0.0"` became `"^2.0.0"` while `lib` stayed at 1.0.0. Ignored releases are now dropped before planning, as @changesets does. The same applies to every package `--only` leaves out.
