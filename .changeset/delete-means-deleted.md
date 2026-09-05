---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Deleting a session now removes all of it.

A Cowork session's sidecar sits beside a directory of the same name holding that session's uploads, outputs and audit log — one measured on a real machine was 14 MB. Only the sidecar was removed, so the session disappeared from every listing while its data stayed on disk. The same is true of a session filed under two project directories, which has two transcripts here: removing the newer one left the session still listed and still resumable.

Both are now taken with the session. This is deliberately unlike `doctor`'s repair of a split session, which *parks* the extra copies rather than deleting them — that runs on a session you want to keep, and refuses outright when the copies have diverged. Deleting is something you asked for explicitly, having already been refused while the session was running and asked to confirm.
