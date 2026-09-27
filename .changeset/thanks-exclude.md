---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With `@changesets/changelog-github`, the "Thanks @…!" on each changelog line now skips the same people the Contributors section does: bots, and anyone in `"contributors": { "exclude": [...] }`. Add your own login there and your changelog stops thanking you on every line. The pull request and commit links stay. Set `"excludeBots": false` to thank bots again.
