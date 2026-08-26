---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

changelog entries are now grouped by what a change *is* rather than by the order its file happened to sort in. Give a changeset a conventional type and scope — `feat(rig): …`, or `type:`/`scope:` frontmatter — and the type picks the section while the scope becomes the bullet's lead-in and groups that tool's entries together. `changerig add` infers the scope from the files your branch touched, `changelogScopes` in config sets which tool leads, and leaving the bump off a typed changeset lets the type decide it.
