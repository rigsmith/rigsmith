---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

A changelog generator configured as a `[name, options]` tuple now receives its options, as @changesets passes them to `getReleaseLine`: `"changelog": ["./scripts/changelog.js", { "style": "terse" }]` sends `{ "style": "terse" }` as the request's `options`, in the written changelog and the `version --changelog` preview alike. Before, only `repo` (for `@changesets/changelog-github`) was read, and an external generator never saw its options.
