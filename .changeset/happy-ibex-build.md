---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan` and `publish` now use the registry your workspace routes an npm package to. Before, they ran npm in the package's own folder, where npm doesn't read the workspace root's `.npmrc`. In a pnpm or Yarn workspace that sends its scope to a private registry from the root, `publish-plan` reported versions already there as unpublished, and `publish` would have sent the package to npmjs.com. The registry is now `node.packageSource`, else the package's `publishConfig.registry`, else the nearest `.npmrc` up to the repository root.