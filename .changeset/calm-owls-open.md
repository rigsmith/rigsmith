---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig tag` and `shiprig publish` now create the `CHANGESETS_OUTPUT` (or `--output`) file even when there is nothing to tag, as `changeset git-tag` and `changeset publish` do. An empty file is how changesets/action and shiprig-action learn there were no tags; before, the file was missing, so a release run with nothing to tag warned, and failed when the action ran `shiprig publish` itself.
