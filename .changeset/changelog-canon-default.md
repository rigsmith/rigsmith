---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`"changelog": "@changesets/cli/changelog"`, the value `changeset init` writes, now renders the built-in changelog. Before, it was run as a command path and every changelog render failed, which broke repos migrating from @changesets that kept their config.
