---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With `@changesets/changelog-github`, a changelog entry whose author can't be found no longer reads `Thanks ! -`. As in @changesets, "Thanks …!" is written only when there's a user to thank; the commit and pull request links stay.