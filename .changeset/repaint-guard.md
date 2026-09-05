---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith/ui"
---

One repaint guard for the whole window, instead of the same fix written twice and missing twice.

The status poll runs every five seconds while the window is open, and most panels answer questions that move far more slowly. Rebuilding regardless tears down whatever the reader is in the middle of, and it did — three times, found separately: the activity feed threw away an expanded file list, the session detail dropped what it was showing, and the split-session list vanished a few seconds after **Show them**.

`changed(key, projection)` now decides, and every panel that repaints on the poll asks it: activity, session filing, accounts and the repository.

The last two had never shown the fault but had the same exposure. Accounts carries the tick a launch button flashes for two seconds and the disabled Switch while Claude Code is running; the repository panel has buttons that sit disabled through a repack. A poll landing mid-operation wipes both.

Callers pass a **projection** rather than the whole payload, which is the part worth getting right. Most of these objects carry something that churns — `.git` grows on every sync, timestamps advance — and comparing everything would report a change on every poll: the same bug wearing a disguise. The repository panel compares its numbers *as displayed*, so reclaiming 2 GB repaints and adding a few KB does not.

`force` skips the check for when a panel's own action changed the data and the repaint is the point, and `repaint(key)` forgets a signature for when something outside the data invalidated the screen — a prune rewrites every commit id under an activity feed whose journal entries have not moved.
