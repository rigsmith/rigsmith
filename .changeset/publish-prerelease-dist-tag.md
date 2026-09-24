---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

In prerelease mode, `shiprig publish` now publishes npm packages under the prerelease tag (`next`, say), as `changeset publish` does. It used to pass no tag, so a prerelease went out as `latest` and became what `npm install` picks. A new `--tag <name>` picks another dist-tag outside pre mode.
