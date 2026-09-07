---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

rig stack: `propose --from <branch>` proposes one topic branch of the stackspace instead of everything the prefix carries, so a second fix in the same project can be its own pull request rather than showing the first one's changes too. Keep each in-flight fix on a branch rooted where that member was imported — `stack-pr-<name>` is the recommended naming, and a bare `--from reader-wedge` finds it — while `main` merges them and stays the fused line you build and test. An exact branch name always wins, so the convention stays a suggestion.

A topic rooted on the import holds upstream plus its own change and nothing else, so there is no patch to replay and nothing that can fail to apply as histories intertwine. Branch off a line that already carries another unmerged fix and the topic contains that fix too, so `propose` lists the commits it is sending and you see the unexpected subject before a reviewer does.

A `pull` leaves topics behind: their trees are upstream as it used to be, and proposing one would present everything upstream landed since as reverted. `propose --from` now refuses such a topic, and `pull` and `status` name them so you find out when it happens rather than days later. They are not re-rooted for you — an import merges into your integration line, which has already merged your topics, so any base derived from it would fold the fix back in.

`--from` needs `trackBranch` and keeps it current with everything the prefix holds, because `rig stack init` rebuilds from it. `rig stack status` also reports how many commits a prefix diverges by and lists the `stack-pr-*` topics in flight.