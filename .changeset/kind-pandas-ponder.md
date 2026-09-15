---
type: fix
"github.com/rigsmith/rigsmith"
---

A tool that is new to winget no longer holds back everyone else's update.

winget's first submission for any package has to be made by hand, because komac updates a published manifest and a package winget has never seen has nothing to update. That failure used to abort the whole submission run before anything was sent — so the next release would have published no winget update at all, for any of the five CLIs, on account of `codexrig` being new. It is now skipped and named in the log, and every published package still goes out.
