---
"github.com/rigsmith/rigsmith": minor
---

`rig build <name>` now disambiguates duplicate project names by path, just like `rig run`. When the same project is checked out in more than one location (e.g. a nested worktree) and its name matches several targets, `build` opens a picker on a TTY, or off a TTY lists the candidate paths and returns an actionable error — instead of silently falling through to the repo-root build command.
