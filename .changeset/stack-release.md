---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

a release can be cut from a `rig stack` stackspace. `shiprig` reads the stack manifest and treats every path under a member prefix as not its to write: `version` still computes the numbers, cascades them, consumes the changesets and writes the changelogs, but stamps nothing into a member's manifest — the number is recorded in `.changeset/versions.json`, read back as the current version next time, and handed to the build as `${version.<pkg>}` — and a member's notes go to the root `CHANGELOG.md`, one section per member. `tag`, `push` and the forge `release` are skipped in the plan, with the reason: a fused history is never tagged or pushed. `version --no-stamp` (or `"versioning": { "stamp": false }`) records instead of stamping anywhere.
