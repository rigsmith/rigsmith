---
"github.com/rigsmith/rigsmith": minor
---

`rig build <name>` now disambiguates duplicate project names, just like `rig run`. When the same project is checked out in more than one location (e.g. a nested worktree) and its name matches several targets, `build` opens a picker on a TTY; off a TTY it lists the candidate paths and returns actionable guidance (narrow the name, run `rig build` from the target directory, or exclude the extra copies in `.rig.json`) — instead of silently falling through to the repo-root build command.
