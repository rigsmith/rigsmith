---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The window gains a **Repository** panel: what the sync repo costs, what it is made of, and the two ways to make it smaller.

The window grows a **Repository** panel carrying the same numbers, with the ratio flagged amber once history costs four times the content, and a prune dialog offering a week, a month or three, and a Repack button beside it. Both report progress inside the panel rather than in the banner at the top of the pane — repacking gigabytes takes a minute, and a message that far from what you are looking at is a message nobody reads; silence for a minute reads as a button that did nothing.

Finishing either one refreshes the rest of the window, which the poll would not have done on its own: the journal has not moved, so the activity feed's repaint check would keep the stale render, and the status line's last sync is read from a commit a prune has just replaced. A prune also clears the cached commit file lists, since it rebuilds every commit above the cutoff and every cached id is then a lie.

The window shows the same split with a bar per row, because the shape of this data is one line at 97% and everything else rounding to zero — a column of numbers makes you work that out, a bar does not. Hovering a row names what it is and how many files it covers.
