---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`init` offers commit and pull request links on a GitHub repository: `--changelog github` writes `["@changesets/changelog-github", { "repo": "owner/name" }]` for the repository's GitHub remote, `--changelog default` keeps @changesets' plain layout, and without the flag it asks at a terminal. A scripted `init` keeps the plain layout and prints how to switch. The docs gain a section on when to use `changelog-github` and what it needs (`gh` signed in for PR and author lookups).
