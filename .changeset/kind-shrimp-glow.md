---
"github.com/rigsmith/rigsmith": minor
---

clauderig: new `clauderig ledger` command reports what the session record holds, and `clauderig ledger backfill` recovers entries for sessions that were pruned before the record existed, reading them out of the sync repo's git history. Run it once after upgrading — and before the repo next squashes its history, which prunes what backfill reads.