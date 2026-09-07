---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

rig stack: `rig stack status` now lists each member's topic branches under it and says where each one went — the branch on your fork carrying its pull request, whether that branch has moved since it was proposed, whether a pull left the topic behind, or that it has not been proposed yet. Recorded in the manifest as `proposals`, written by `propose --from`.

`lastPropose` could not answer this. It holds one branch per repo and is overwritten on every propose, which was enough while a proposal meant the whole prefix and only one could be in flight; two topics for one member are two pull requests. It keeps its own job, naming the branch a rebuild reconstitutes from.

Only what git cannot be asked is recorded: the fork branch and the commit that was sent. Which topics exist, which member each touches and whether a pull left one behind are derived from the repository every time, so a branch deleted after its pull request merged stops being listed rather than leaving the manifest claiming work that is not there. The commit is what keeps a topic recreated under a previously-used name from inheriting the old destination.