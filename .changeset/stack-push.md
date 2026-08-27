---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack push` sends a project you own back to its own repository with its history intact. `send` exists for someone else's project: it flattens a directory into one commit on a branch of your fork, which is exactly what a reviewer wants and exactly wrong for a repository that is yours, where it discards every message, author and bisect point on the way out. `push` fast-forwards the project's own branch instead, so each workspace commit that touched it arrives as its own commit — and a change spanning several projects lands as a matching commit in each of them, while commits that touched nothing there do not appear at all.

It works by running the exact inverse of the filter the repo was imported with, so the history shared with upstream comes back as upstream's own commits and what you added sits on top as a fast-forward. Mark a project `"owned": true` to enable it; that cannot be guessed, because an ordinary fork arrangement is indistinguishable from a repository you happen to own. A pinned project is refused, since a pin names a fixed point and there is no branch there to move, and a project whose upstream has moved since you last pulled is refused for the same reason `send` refuses it.
