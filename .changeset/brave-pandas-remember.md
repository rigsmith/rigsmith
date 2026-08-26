---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

sessions are now dated by the last record inside the transcript rather than by the file's timestamp, which copying rewrites. A restore or a checkout of the synced repo used to re-date hundreds of old chats to today and bury the ones you actually used. Affects `recent`, `search` ordering and the `--since`/`--until` filters; a transcript with no dated record is marked `~` rather than guessed at.
