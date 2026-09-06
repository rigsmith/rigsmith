---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

A session filed in more than one place now says so in its own row, and offers the repair where you are already standing.

The status pane has listed split sessions for a while, but only to someone who thought to go and ask it. The person who needs it is looking at the sessions list, trying to work out why a conversation appears to have lost a week — which is what a split looks like from the outside, because anything resolving by project directory can open the copy that stopped growing.

The row carries a small mark, on the same rule as the store icons beside it: shown only when it is true, so a row with nothing on it is the ordinary case. Opening the session describes both copies — how many records each holds, and how many exist only in the older one — and offers **Keep newest, park the older**, the same repair the status pane offers, moving the older copy to `~/.clauderig/parked` rather than deleting it.

When the copies have genuinely diverged it says so and offers no button. Choosing between them would lose turns, and that is a decision for the person who was there.

The comparison is loaded when you open the session, not when the list is drawn: describing a split reads both transcripts end to end to match them record by record, which is not something to do because a list scrolled past.
