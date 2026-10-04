---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publish-plan` and `publish` now use the registry your workspace routes an npm package to: `node.packageSource`, else the package's `publishConfig.registry`, else the nearest `.npmrc` up to the repository root. Before, a workspace routing its scope to a private registry in the root `.npmrc` had published versions listed as unpublished, and `publish` would have sent the package to npmjs.com. A registry given as an unset or empty `${VAR}` is now an error. OIDC trusted publishing is used only for npmjs.com (or `node.packageSource`); a package routed elsewhere publishes with npm's own auth for that registry and is never sent the CI token.
