---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`doctor` now reports every problem it finds instead of stopping at the first. That includes changeset files that can't be read, which block `status` and `version`: each one is listed with its error.
