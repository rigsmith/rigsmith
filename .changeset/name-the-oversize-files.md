---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The sync journal now records **which** files the size cap left out, and how big they were.

A row in the activity feed reading `Synced 5 files, 2 files too large` names a conversation that did not get backed up without saying which one. `clauderig sync` prints the paths as it runs, but the journal — the record that exists so an outcome survives the process that produced it — kept only a tally, so an hour later there was no way to find out. That is worse than the equivalent gap in the redaction count, because a redacted file is still in the repo and an oversized one is not there at all.

The size comes with it. "Too large" invites exactly one question, and the answer decides what to do: a transcript a little over `maxFileBytes` is an argument for raising the cap, one at ten times the cap is an argument for leaving it behind.

The list is capped at 25 files per record, like the redaction list, so a first sync over a tree of marathon transcripts cannot write a record longer than anything will show. The count stays the true total.
