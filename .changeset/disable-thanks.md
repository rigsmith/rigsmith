---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`@changesets/changelog-github`'s `disableThanks` option now works: `["@changesets/changelog-github", { "repo": "owner/repo", "disableThanks": true }]` drops "Thanks @…!" from every changelog line and keeps the pull request and commit links. Before this, rig ignored the option.
