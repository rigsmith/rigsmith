---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig packages` (including `packages list --json`) and `shiprig doctor` work in a repo with no `.changeset/` directory, listing every package with nothing releasing, where they used to fail reading changesets. `shiprig release` in such a repo now skips the `version` step as having nothing pending, so a publish-only release works. `changerig status` and `version` still require `.changeset/`, as `changeset status` does.