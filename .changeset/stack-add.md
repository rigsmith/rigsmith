---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack add` puts a repo into a stackspace without hand-editing the manifest. Give it a repo — as `host/owner/name` or the URL you happen to have — and it works out the directory, writes the entry, and imports it. With no argument it asks; every answer is also a flag, so the same thing scripts. `--owned` marks a repo as yours rather than a fork you contribute to, which is what makes `rig stack push` apply to it, and a repo of your own needs no separate fork.

A manifest with no repos in it is now a state rig accepts rather than an error, which is what makes the first `add` possible: it is exactly what `rig stack init` scaffolds. The verbs that act on repos still say so when there are none, and the `rig ui` menu offers only the two things that can be done in an empty stackspace rather than listing seven that cannot.
