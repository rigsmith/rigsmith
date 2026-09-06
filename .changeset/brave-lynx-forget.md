---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Forget a deleted `.gitattributes` before reading Git attributes.

`git check-attr` falls back to the index for a file the working tree no longer
has, so a `.gitattributes` that stops being synced goes on governing the backup
after it is deleted — invisible, refusing every publish, and with nothing left
on disk to remove.
