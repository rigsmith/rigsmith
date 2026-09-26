---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`version --only <package>` releases just the packages you name and leaves the rest for later, so you can ship one part of a monorepo at a time. Packages that have to go out together form a group, which `status --output` shows: name every package in the group, or `--only` stops before changing anything.
