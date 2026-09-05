---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Two sessions ending at the same moment no longer sync on top of each other.

`clauderig sync --flush` is meant to skip the debounce — a session that is ending has no later turn to defer to — but it skipped the lock along with it, so two SessionEnd hooks firing together could stage, commit and push the same tree at once. The lock is now taken by every automated sync and only the interval check is skipped; a flush waits briefly for a sync already in progress rather than running beside it.

A sync that overran the lock's twenty-minute lifetime also used to delete whichever lock had replaced its own on the way out, letting a third run overlap after all. Each holder now removes only the lock it wrote.

Related: `clauderig hooks install` listed two commands for the `Stop` event, so an install could only ever satisfy one of them and `clauderig doctor` reported drift against the other for ever after. And a failed first clone no longer creates the staging directory it was cloning into — git refuses to clone into a non-empty directory, which turned one transient network failure into every subsequent pull failing too.
