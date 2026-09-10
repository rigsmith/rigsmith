---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig typecheck --all` no longer stops at the first workspace package that has no `typecheck` script — that package is now listed as skipped and every other package still runs. Same for `build`, `test`, `format`, `lint`, `clean` and rebuild's All packages. A script that runs and fails is still a failure and still fails the run; a run ends with a count like `✓ 34 ok – 1 skipped`.