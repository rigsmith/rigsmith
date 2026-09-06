---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Never sync a `.gitattributes` from inside a synced tree.

The backup is a Git repository, so one of these arriving as ordinary content
stops being content and starts governing how the backup stores every file
beside it. One ships inside a plugin marketplace clone reading
`* text=auto eol=lf`, which re-enables exactly the byte conversion the backup
exists to prevent — publication refused, and deleting the file did not help
because the next plugin update brought it back. It is now pruned by name at any
depth, like `node_modules`.
