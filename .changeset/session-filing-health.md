---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

clauderig now notices when a session is filed in two places, and says so in all three front ends.

A conversation that continues in a different directory gets a **second transcript**: Claude Code files by working directory, so it opens a new one under the new slug while the first stays frozen at the moment the work moved. Both are real files with the same id. Anything resolving by directory — Claude Desktop, in particular — can open the frozen one, which looks exactly like the session losing everything after that date.

**The tool was making this worse.** `TranscriptPaths` kept whichever copy the directory walk reached first and discarded the rest silently:

```go
if _, seen := paths[id]; !seen {
    paths[id] = p        // first in walk order wins
}
```

That is alphabetical luck. A session whose stale copy sorted first was reported from the day it split, with nothing to indicate a complete copy existed. On a real machine this went the right way only because an uppercase directory name happened to sort before a lowercase one.

The winner is now the copy whose **last record is newest**, ties broken on size and then path so two runs never disagree. The extra read only happens on the rare id that has more than one file, so the ordinary case costs nothing.

The copies not chosen are **kept and reported** rather than dropped. `sessions.CheckHealth` finds both this and the fault it presents as — a Desktop sidecar naming a directory that no longer holds the transcript — and all three front ends call it:

- **`clauderig doctor`** — a `session filing` check naming the sessions and the slugs holding them
- **The dashboard** (`clauderig` with no arguments) — a warning line, only when there is something to say
- **The window** — a banner above the Repository panel, silent otherwise

One implementation, because three would eventually disagree, and disagreeing about whether a conversation is intact is the worst thing they could differ on.

In the window the banner expands: **Show them** lists each split session by title, with both copies side by side — the project each sits in, how many records it holds, its date range, and how many records exist *only* in the older one. Where the older copy is wholly contained in the newer, one button parks it.

Parked, never deleted, into `~/.clauderig/parked/<timestamp>/` — and deliberately outside `~/.claude`, because a file parked there would be picked up by the next sync and handed back by the next restore, which is how the copy returns.

Describing a split reads *both* transcripts and compares them record by record, so it is not done on the status refresh — the banner is cheap, the list is asked for.

It reports and never repairs by itself. Which copy is wanted is usually obvious and occasionally not: when two have genuinely diverged, each holding turns the other lacks, discarding either loses a conversation. When they have diverged the button is absent and the row says why, with the count of records that exist only in the older copy — a missing button is a mystery, and this is precisely the case where a person has to look. There is no flag to override it: "this copy holds three turns the other lacks" has no safe automatic answer, and offering one in a window is how somebody loses a conversation by clicking. `clauderig reroot <session> <dir>` re-files one deliberately.

Found four such sessions on the machine it was written on.
