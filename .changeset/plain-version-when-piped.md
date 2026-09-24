---
type: feat
"github.com/rigsmith/rigsmith"
---

`--version` prints the bare version number (`1.20.3`) when its output isn't a terminal, so a script or CI step can read and compare it, as with `changeset --version`. In a terminal it still shows the banner. This applies to every rig.
