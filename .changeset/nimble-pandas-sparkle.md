---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

changerig now matches @changesets v3. Changelogs put a blank line under each version heading, and a release with nothing of its own reads "No changes in this release." A peer dependent that falls out of range gets a patch, not a major. Prerelease mode moves consumed changesets into `.changeset/pre/` and keeps only the mode and tag in `pre.json`; a prerelease started on an older version migrates on the next `version`. Two changes need action: `version` with nothing pending now exits 1, and private packages are no longer versioned unless the config sets `"privatePackages": { "version": true }`.