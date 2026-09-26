---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`status --output` now writes the full release plan, the same shape as `changeset status --output`: each changeset with the packages it names (including `none`), and each release's old and new version. Useful for scripts and CI.
