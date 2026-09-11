---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The tray lists every open Claude Desktop window by name, and clicking one brings it forward.

Two Claude Desktop windows look identical in the Dock. Each instance does get its own tile, but they carry the same icon and the same name, so telling the work profile from the machine-wide app means clicking one and looking — and nothing in macOS can badge another application's tile, since the tile belongs to that process. The **Claude Desktop** submenu names them instead: each profile, the machine-wide app, and any window running on a directory outside the store.

Raising is by process id rather than by activating the application, which is what the Dock already does and is the ambiguity being solved — every instance is one application to the OS, which then picks the window itself. On macOS that needs Automation permission, so the first raise prompts.

The menu is rebuilt only when the set of windows changes, and the pid is re-checked against the live process list before anything is raised: it comes from a menu built up to ten seconds ago, so a window that has closed raises nothing rather than dragging whatever inherited its pid to the front. A process scan that fails says so, rather than reporting an empty machine.

**Open the main app** at the bottom runs `clauderig desktop main`, which decides for itself whether to launch or raise, so the item works whether or not that window is there.