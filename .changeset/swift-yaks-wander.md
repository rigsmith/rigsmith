---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

the changelog now groups by what a change *is*, not by the order its file happened to sort in. A changeset can carry a conventional type and scope — `feat(rig): …`, or `type:`/`scope:` frontmatter — the type picks the section, the scope becomes the bullet lead-in and groups that tool's entries together. Features lead, fixes follow, internal work sinks to the bottom.

`changerig add` infers the scope from the files your branch touched, so the easiest thing to forget is the thing you no longer type; `--scope -` says a change belongs to no one tool. Leave the bump off a typed changeset and the type decides it, per package — an explicit bump still wins where one changeset moves two packages differently.

`add` also no longer writes a changeset naming no packages. That file was ignored by every later step while `add` reported success; with one package it is used, with several you are asked to name them.