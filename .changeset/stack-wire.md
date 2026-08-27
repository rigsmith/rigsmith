---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack wire` writes a stackspace's build overlay for you. It works out which package references cross from one member to another — those are the ones that would otherwise be fetched from a registry — and points them at the sources instead, taking the answer from what the ecosystem adapters already know about the projects rather than from anything you have to list by hand. Adding a member is a re-run.

No project file changes, so a member cloned on its own still builds from packages exactly as it did. It rewrites its own file and refuses to touch one you wrote yourself, saying so rather than merging blindly. Anything it finds that would stop the redirects taking effect — most importantly a member whose own build file ends MSBuild's search, so everything under it silently keeps resolving published packages — is reported by both `wire` and `rig stack doctor`, which reports without writing.
