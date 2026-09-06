---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The window shows sessions filed in more than one place, and offers to resolve them.

A banner appears above the Repository panel when `clauderig` finds any — silent otherwise, because a panel that says "all good" every time is a panel people stop reading, and this one has to be noticed the once it matters.

In the window the banner expands: **Show them** lists each split session by title, with both copies side by side — the project each sits in, how many records it holds, its date range, and how many records exist *only* in the older one. Where the older copy is wholly contained in the newer, one button parks it.

The panel is only rebuilt when the finding actually changes. The status poll runs every five seconds while the window is open, and this data moves only when a session is filed or fixed — repainting regardless threw the expanded list away underneath whoever had just opened it. Fixing one keeps the list open, since collapsing it the moment you resolve one of four is not a reward for resolving it.

Describing a split reads *both* transcripts and compares them record by record, so it is not done on the status refresh — the banner is cheap, the list is asked for.
