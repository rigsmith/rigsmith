---
type: fix
---

`clauderig sync` no longer lets one runaway transcript wedge the whole sync. A single marathon session can produce a `.jsonl` of several hundred MB — GitHub warns past 50 MB and refuses the push outright past 100 MB, so one such file took every other file down with it and the sync could never complete. Files above `retention.maxFileBytes` (default 50 MB, under the warning rather than at the cliff) are now dropped, and any copy an earlier uncapped sync had already staged is removed, so the cap can dig an existing repo out of the hole it was added to fix. Dropped files are named in the sync output rather than silently omitted — they're whole conversations, not incidental churn. A config predating the setting gets the default; disabling the cap takes an explicit negative value.
