---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Repos moving over from Changesets that kept `"changelog": "@changesets/cli/changelog"` in their config now get the normal changelog. Before, every changelog failed to render.
