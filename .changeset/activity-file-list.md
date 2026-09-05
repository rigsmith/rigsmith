---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith"
---

Click a row in Sync Activity to see the files that run touched, with added and removed line counts.

Each sync is exactly one commit in the staging repo, so the answer already existed losslessly — it just had no way of being asked. Reading git rather than recording paths in the journal keeps a bounded feed from turning into an inventory of a data set git already holds.

A record and its commit are matched on time: the journal line is written *before* the commit precisely so it travels inside it, which means the sha does not exist yet when the record is made and there is nothing to record. The commit always lands a moment after its own record, so "the earliest commit at or after this timestamp from this machine" is exact. A run that committed nothing says so rather than erroring — that is the ordinary state of a sync with nothing to write, and of the newest record, which is usually still waiting for its own commit.

Long lists are capped at 200 paths and say how many they dropped; a first sync on a new machine would otherwise hand the window several thousand rows.
