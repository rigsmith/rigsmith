---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack doctor` and `rig stack wire` now name every package that crosses from one member of a stackspace to another, and which way it goes, instead of counting them. A count cannot be checked; a list can, and the link people most often miss is library to library, where the app is not involved and nothing looks wrong.

Both also flag a fused repo whose packages nothing else there consumes. That is nearly always one of two mistakes and silent either way: the wrong repo was fused, or the right one was and the code has since moved to a fork that renamed the package. A package is matched by identity rather than by where it came from, so a stackspace like that imports, wires and builds while changing nothing at all. A project you own is left alone, since an app is meant to be the end of the graph.

And where a member carries its own root build file, which ends MSBuild's search and quietly leaves everything under it resolving published packages, `wire` now patches it — but only for a member marked `"owned": true`. That file is yours and the line belongs committed to it. For a fork you contribute to it is reported instead, because the same patch would ride into somebody else's pull request as plumbing they never asked for.

Two things that got in the way of doing this at all. `send` and `push` refused whenever anything in the stackspace was uncommitted, including files outside every member — so `rig stack wire` writing the stackspace's own overlay blocked a push that could not possibly contain it. They now refuse only for uncommitted work inside the project being exported, which is the case that matters: that work would be silently left out of what leaves. `init` and `pull` keep the whole-tree check, and should, since an import amends its merge commit and stages everything.

And `rig stack push` no longer needs the repo named when exactly one project in the stackspace is yours, since only a project you own can be pushed and there is nothing to disambiguate. With several it still asks, because this is the verb that writes to a remote.
