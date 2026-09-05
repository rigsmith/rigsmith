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

It reports and never repairs. Which copy is wanted is usually obvious and occasionally not: when two have genuinely diverged, each holding turns the other lacks, discarding either loses a conversation. `clauderig reroot <session> <dir>` re-files one deliberately, which is a decision rather than a repair.

Found four such sessions on the machine it was written on.
