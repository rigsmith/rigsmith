---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan` and `publish` now use the registry your workspace routes an npm package to: `node.packageSource`, else the package's `publishConfig.registry`, else the nearest `.npmrc` up to the repository root. Before, npm ran in the package's folder and missed a root `.npmrc`, so in a pnpm or Yarn workspace with a private registry `publish-plan` listed published versions as unpublished, and `publish` would have sent the package to npmjs.com. A registry given as an unset `${VAR}` is now an error rather than a quiet fall back to npmjs.com.
