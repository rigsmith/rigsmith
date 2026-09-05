---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A sync that changed nothing now says so, instead of reporting the same file count as every other sync.

JSON files can't be skipped on timestamp the way plain copies can — each one is read, redacted, portablized and re-marshalled on every pass, so there is no cheap way to know up front whether the result will differ. They were therefore written and counted every time, whether or not a byte moved. That made the "files synced" figure a property of the tree rather than of the run: it barely changed between syncs, so `clauderig status` and the UI's activity feed showed an unbroken column of one identical line, and the runs that did something were invisible among them. The produced bytes are now compared against the staged copy, and an identical result is left alone.

So the journal records what a run did rather than what it looked at. Quiet runs — the common case, since syncs fire every few minutes against a tree that only moves when you are working — read `No changes — 1,035 files already current`. Redaction totals are no longer repeated on them, for the same reason they were noise: the redactor runs over the whole tree every pass, so its count says nothing about this run. Pruning and oversize refusals are still reported either way, because those happen to the staged copy whether or not anything new was written.

It also stops rewriting a couple of thousand files an hour to produce identical output.
