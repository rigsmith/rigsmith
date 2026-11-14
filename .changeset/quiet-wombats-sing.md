---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

npm provenance no longer needs OIDC. `node.provenance` in `.changeset/config.json` decides it whatever the publish authenticates with: `auto` (the default) attests whenever the run is in GitHub Actions with an id-token (`id-token: write`), so a token publish, which every new package's first publish is, gets provenance too; `always` attests or fails the publish before anything is uploaded; `off` never attests. Whenever shiprig doesn't attest it passes `--provenance=false`, so an `.npmrc`'s `provenance=true` can't turn it back on. npm attests only a package published with access `public` or already public on the registry, and only from a public GitHub repository, so `auto` skips any other and `always` fails on it before the upload. A token publish attests with npm 9.5.0 or newer; an OIDC publish still needs npm 11.5.1.