---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack doctor` and `rig stack wire` now name every package that crosses from one member of a stackspace to another, and which way it goes, instead of counting them. A count cannot be checked; a list can, and the link people most often miss is library to library, where the app is not involved and nothing looks wrong.

Both also flag a fused repo whose packages nothing else there consumes. That is nearly always one of two mistakes and silent either way: the wrong repo was fused, or the right one was and the code has since moved to a fork that renamed the package. A package is matched by identity rather than by where it came from, so a stackspace like that imports, wires and builds while changing nothing at all. A project you own is left alone, since an app is meant to be the end of the graph.

And where a member carries its own root build file, which ends MSBuild's search and quietly leaves everything under it resolving published packages, `wire` now patches it — but only for a member marked `"owned": true`. That file is yours and the line belongs committed to it. For a fork you contribute to it is reported instead, because the same patch would ride into somebody else's pull request as plumbing they never asked for.
