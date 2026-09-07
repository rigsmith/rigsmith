---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

rig stack: `propose --from <branch>` proposes one topic branch of the stackspace instead of everything the prefix carries, so a second fix in the same project can be its own pull request rather than showing the first one's changes too. Keep each in-flight fix on a branch rooted where that member was imported — `stack-pr-<name>` is the recommended naming, and a bare `--from reader-wedge` finds it — while `main` merges them and stays the fused line you build and test. An exact branch name always wins, so the convention stays a suggestion.

A topic rooted on the import holds upstream plus its own change and nothing else, so there is no patch to replay and nothing that can fail to apply as histories intertwine; a topic based on another unmerged fix is refused rather than carrying it along. `--from` needs `trackBranch` and keeps it current with everything the prefix holds, because `rig stack init` rebuilds from it and would otherwise quietly build without the fixes you left out.

`rig stack status` now reports how many commits a prefix diverges from upstream by — a plain `propose` sends all of them, and nothing said so — and lists the `stack-pr-*` topics in flight.