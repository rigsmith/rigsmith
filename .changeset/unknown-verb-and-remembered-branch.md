---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

A verb rig does not have is now refused instead of quietly succeeding. `rig stack` and `rig config` open a picker when run bare, and an unrecognised subcommand was being handed to that picker — so a verb this build had never heard of printed a menu and exited 0. The cost lands on whoever is furthest behind: `rig stack wire` on a rig that predates it reported success and wired nothing, and a stale build showing the picker for `rig stack add` reads as a broken feature rather than an old binary. Both groups now name what you typed, list the verbs they do have, and suggest checking `rig --version`, since an out-of-date build is a likelier cause than a typo and the one nobody thinks to check. Running either bare still opens the picker.

`rig stack propose` also remembers the branch you last used for a repo and offers it back. Proposing again to the same branch is how an open pull request takes review feedback, so that name is usually wanted several times: the prompt now arrives with it filled in, and one keypress updates the pull request already there. Without a terminal it is reused rather than refused, so a second round scripts as `rig stack propose some-lib`.
