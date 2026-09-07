---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack setup` — the one command a freshly cloned stackspace needs.

It installs the fusion engine, reconstitutes the members, writes the build
overlay and prints the status, in an order where each step can see what it is
judging. The obvious order did not: `doctor --fix` is what installs the engine,
so it had to run first, and with no members imported nothing crosses between
them — so the overlay looked left over and `doctor` advised deleting it. `wire`
went further and deleted it. Both now say the members are not imported yet and
point at `setup`.

`rig stack init` also writes a `README.md` when the repository has none,
generated from the manifest: what a stackspace is, the member table, how to set
it up, and the two things a seed cannot show — the directories are absent on
purpose, and work leaves through `propose` rather than a push. Edit it and rig
leaves it alone.
