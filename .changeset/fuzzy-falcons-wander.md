---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig publishes a pnpm workspace with real versions in place of `workspace:` ranges. In a pnpm workspace (a `pnpm-workspace.yaml` or `pnpm-lock.yaml` at or above the package), `publish` and `pack` have pnpm pack each npm package, so `"workspace:*"` ships as the exact version, and npm publishes that tarball with the same auth, OIDC, provenance and registry as before. The publish scripts still run, each once, in npm's order (`prepublishOnly`, pnpm pack's `prepack`/`prepare`/`postpack`, then `publish` and `postpublish`), with `shiprig pack` and `--from-pack-dir` too, unless `ignore-scripts` is set. Before, npm published the ranges as written, which no registry can resolve. Outside a pnpm workspace a `workspace:` dependency is now refused, a dry run included, rather than shipped. With a custom `steps.publish` action in the release config, `shiprig publish` now says that it doesn't run it (`shiprig release --only publish` does) instead of ignoring it silently.