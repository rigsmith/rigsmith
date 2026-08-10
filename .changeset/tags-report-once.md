---
type: fix
"github.com/rigsmith/rigsmith"
---

`shiprig publish` and `shiprig tag` report each git tag once, instead of once per package sharing it.

Both tagging loops iterated packages and rendered a tag for each. A `tagTemplate` like `v${version}` collapses every package in the repo onto one tag, so a 12-package repo printed `would tag v0.2.0 → push origin` twelve times for the single tag it would create. A real run was worse in a quieter way: one `tagged+pushed` followed by eleven `tag exists`, because each iteration re-read the tag the previous one had just created — reporting a tag it made as pre-existing. `shiprig tag`'s summary counted the same way, so "12 tag(s)" meant one.

Both loops now skip a tag they have already handled this run, so the output has one line per git ref and the counts match what the run actually did. A tag left over from a *previous* run still reports `tag exists`, once.

Covered by an end-to-end `publish --dry-run` over three packages sharing `v${version}`, asserting the tag is reported exactly once (three times before the fix).
