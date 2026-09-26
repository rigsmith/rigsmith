---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`status --output` writes @changesets' whole release plan, not only its releases. It adds a `changesets` list, where each changeset has its `id`, `summary` and every package it names with its bump, `none` included. Each release gains its `oldVersion` and the `changesets` ids that name it (an empty list for a release a dependency drives). In prerelease mode, `preState` carries the prerelease state. A tool reading the plan can now see a `none` decision, which the releases list can't show. The one difference from canon: a summary written with a conventional prefix (`feat: …`) comes without it.
