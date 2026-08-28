---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig` starts immediately, and stops searching directories that are not projects.

Run in a large directory — a home directory, a mounted share — bare `rig` showed nothing at all for a long time and then opened a menu. Two separate faults, both of which had to go.

**It searched places that hold no project.** `detect.Root` walks up looking for `.rig.json`, a workspace manifest or `.git`, and returns the starting directory when it finds none — which reads exactly like finding one there. So in `~` or `C:\Users\John`, rig concluded the home directory *was* the repo root and went looking for projects in it, which means walking the whole thing.

There is now `detect.FindRoot`, which also reports whether anything actually anchored the root. When nothing does, the menu says so and offers what still makes sense — `init`, `doctor`, `self-update` — and does no searching at all. No ecosystem detection, no capability probe, no tree walk. Build verbs are left out on purpose: they would have nothing to act on, and a menu whose entries all error is worse than a short menu that is honest.

**And it did all of that before painting anything.** Discovery ran inside `newMenu`, so the banner appeared only once the filesystem was done — the slower the machine, the longer the tool looked broken. `newMenu` now returns immediately and discovery runs as a bubbletea command, so the banner is on screen at once with a line naming the directory being searched. Only `q` and `ctrl+c` are accepted while it works: a keystroke that silently does nothing is worse than one ignored on purpose.

Project discovery also takes a two-second budget. Even inside a real repository a tree can be enormous, and the menu waits on it — stopping early finds fewer projects, which is a worse menu, while not stopping finds them eventually, which is no menu.

One existing test claimed to cover "a repo with no recognized ecosystem" while its fixture was a bare temp directory that was never a repo. It passed because nothing could tell those two apart — the confusion this change is about. Its fixture now creates the `.git` its comment always implied.
