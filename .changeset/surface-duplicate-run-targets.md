---
"github.com/rigsmith/rigsmith": minor
---

Surface duplicate-named projects consistently, and let `rig run <name>` pick between them. When the same project is checked out in more than one path (most often a nested git worktree under `.claude/worktrees/`), discovery now keeps every copy instead of silently collapsing them by name: `rig info` shows each copy's path, and `topoSort`-backed views (`--all`, the workspace pickers) list them all. `rig run <name>` that matches several projects now opens a picker (name · ecosystem · path) instead of falling through to a bare `dotnet run <name>` that failed with a misleading "Couldn't find a project to run." Off a TTY it lists the matching paths and errors actionably. Name resolution also gained .NET dot-short matching (`App2` → `Tweed.App2`), matching how `defaultProject` already resolves.
