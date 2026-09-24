---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`version --ignore <package>` (repeatable) leaves packages out of one run, as `changeset version --ignore` does: their changesets wait for a later run. As in @changesets, it takes exact names, can't be combined with `ignore` in the config, and every `version` run now refuses to skip a package that a published package depends on unless that dependent is skipped too.
