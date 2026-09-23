---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`status --since` narrows commit-sourced releases too: with `versioning.source` of `commits` or `both`, only the commits the branch adds since the ref count, as only its changeset files already did. `version --changelog --since <ref>` previews just the branch's share of the changelog (`--since` needs `--changelog` or `--dry-run`).
