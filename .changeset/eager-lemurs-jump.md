---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig packages list --json` prints every discovered package for a script: its ecosystem, directory, current version, next version and bump when it releases, whether it is private or ignored, and the `CHANGELOG.md` its notes go to (with the section title when a stackspace shares the root file). Paths are relative to the repository root.