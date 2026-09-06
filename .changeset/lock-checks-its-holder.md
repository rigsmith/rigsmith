---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A sync killed mid-run no longer stops the machine syncing for twenty minutes.

The lock a sync holds records the process holding it, but the check that breaks an abandoned one only asked how old it was. So a hook terminated with its shell — which happens whenever a session ends mid-sync — left a lock that every later sync honoured for the full twenty minutes, reporting `another sync is running — skipping` while nothing was. Seen twice inside a quarter of an hour on one machine.

It reads the pid that is already in the file. A lock whose holder has exited is stale whatever its age, so the next sync takes it immediately. This can only ever break a lock sooner: a pid that has been reused by something unrelated still answers alive, and the age limit still applies to a holder that is genuinely running.
