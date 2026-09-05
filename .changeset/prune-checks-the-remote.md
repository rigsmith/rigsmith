---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig repo prune` no longer force-pushes over work another machine pushed.

It rewrites history and publishes the result, and it did that without ever looking at what the remote was actually at. A machine that had synced since this one last fetched held commits this history did not, and the force-push deleted them — from the remote, and so from every machine, with nothing to say it had happened.

It now fetches before rewriting anything, refuses outright while origin holds commits this machine does not have (run `clauderig sync` first, then prune), and pushes with a lease against the sha it actually saw — so a machine that syncs during the confirmation prompt gets a rejected push instead of being overwritten. With no remote configured it folds locally and says so, rather than failing after the rewrite.

Two related fixes to which commits it folds: it repacks-then-measures rather than trusting loose objects, and it folds only the leading run of commits that are wholly outside the retention window. Commit dates here are not monotonic — machines sync on their own clocks — and a commit inside your retained window could previously be folded away by an older one that arrived after it.
