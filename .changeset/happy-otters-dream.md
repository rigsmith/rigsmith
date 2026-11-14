---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

A version already on disk can now have its first release. `version --release-as <package>=<current version>` writes that version's changelog entry without bumping anything, when the version has no git tag and isn't on its registry; it needs no changesets, and uses any that are pending, else `--initial-message` or "Initial release". A version that is tagged or published is refused. shiprig-action's `releaseAs` passes the same flag.