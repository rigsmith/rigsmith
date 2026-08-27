---
type: docs
"github.com/rigsmith/rigsmith"
---

The stack design notes now specify a verb for exporting a workspace member to a repository you own, with its history intact. `send` deliberately squashes a member's directory into a single commit rooted on the upstream tip, which is what a reviewer wants from a pull request and the wrong thing entirely for your own repository, where it discards every message, bisect point and author on the way out. That trade is tolerable while your own project lives outside the workspace and is pushed with ordinary git, and stops being tolerable the moment the workspace becomes the only supported layout — so the notes record this as a prerequisite for that change rather than a follow-up to it. The design rests on the subdirectory filter being the exact inverse of the prefix filter rig imports with, and the note records a spike confirming that the filtered commits come back byte-identical to upstream's, so a push fast-forwards rather than forking the history.
