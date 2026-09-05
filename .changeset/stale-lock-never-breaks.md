---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A sync that was killed no longer stops that machine syncing for good.

**Anyone on 1.13.0 should take this one.** The lock a sync holds records when it was taken, and 1.13.0 wrote that in nanoseconds while the check that breaks abandoned locks read it as seconds. A nanosecond count read as seconds lands tens of thousands of years in the future, so the lock's age came out negative and it never became stale.

One interrupted sync — a laptop closed mid-run, a hook killed with the terminal — was enough. The lock file stayed, and from then on every automated sync on that machine reported `another sync is running — skipping` and did nothing. The machine looked fine and stopped being backed up, which is the failure clauderig exists to prevent. Nothing was lost: the transcripts are still on the machine, and the first sync after this fix picks up everything since.

Locks written by earlier versions carry seconds and are still read correctly, so an upgrade mid-flight is safe either way.

Found by Codex while reviewing the transcript-chunking work.
