---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`publishDirs` publishes packages a build generated rather than ones anyone checked in.

Some packages never exist in the tree. rigsmith's own npm wrappers are written under `npm/dist/` from the release archives at publish time, and are gone after a clean — so discovery finds none of them and `shiprig publish` published nothing. Name them as repo-relative globs under the ecosystem block in `.changeset/config.json` (`"node": { "publishDirs": ["npm/dist/*"] }`) and they publish like any other package, OIDC trusted publishing included.

They are never versioned — the build that produced each binary already stamped its manifest — and a glob matching nothing is not an error.