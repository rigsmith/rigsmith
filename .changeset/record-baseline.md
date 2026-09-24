---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

With a release record kept (`versioning.record`) and commits as a versioning source, each package's next release counts its commits from the commit that recorded its last release in `.changeset/versions.json`, not from its last tag. It's per package, so releasing one package doesn't move another's starting point, and it wins over a tag, which can be deleted or never pushed; a package the record doesn't hold falls back to its tag. Before this, a package tagged `name@version` (not a module-style `v1.2.0`) counted its whole history again on every release.
