---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig repo` reports what the sync repo costs, and `clauderig repo prune` makes it smaller.

The staging repo is the one part of clauderig that grows without anyone deciding it should. Transcripts are append-only and sync every few minutes, so each run stores another copy of a file that only got longer: history outgrows the content it is history *of*, steadily and invisibly, and the first sign is usually a push getting slow. There has been a size-triggered squash for a while, but it fires on a ratio nobody can see and answers a question nobody got to ask.

`clauderig repo` prints the checkout size and file count, the size and number of commits behind it, the ratio between them, and the span of history still reachable. The ratio is the number worth watching and the one nobody works out by eye — on a real repo here it reads `1.62 GB across 3,391 files` against `2.91 GB in 285 commits`, or 1.8× the content.

`clauderig repo gc` repacks loose objects, reclaiming space without touching a single commit — the remedy to try first, and usually the only one needed. `clauderig repo prune --before 2026-08-01` folds every commit older than that into a single base commit and keeps the ones after it, unchanged — same trees, same messages, same authors and dates. **The checkout does not change**: the files stay exactly as they are and only the record of how they got there is dropped. It rewrites the branch and force-pushes, which is what the automatic squash has always done, so other machines pick the new history up on their next sync.

The kept commits are rebuilt with `commit-tree` rather than replayed with `rebase`. Each carries its exact tree across, so nothing is diffed, there is no conflict to resolve, and a replayed commit cannot end up with a tree that differs from the one it recorded. It also costs time proportional to the number of commits rather than to their content, which matters when a week of syncing is a couple of thousand of them.

Rewriting history other machines share is interactive-only: it confirms in a terminal and refuses outright without one, with no flag to route around that. What it costs you is stated up front — sessions that retention deleted before the cutoff can no longer be recovered by `clauderig ledger backfill`, which reads them out of exactly the history this drops.

It also says when history was last truncated. The oldest reachable commit is not when a repo started if it has ever been squashed — the root is then a commit the squash itself wrote — so reporting that date as the beginning turns discarded history into a repo that merely looks young. A real 2.9 GB repo here claimed to begin the previous morning, because the automatic squash had fired unprompted and nothing said so. A squashed root now reads `squashed <when> — earlier history was discarded` instead of a range.

It also answers the question worth asking — how far back can I go — as a span rather than a date. `retained 28 hours` is read at a glance; `2026-08-27 08:18` makes the reader do arithmetic, and after a squash the answer is usually "less than you think", which is exactly when nobody does it. It will not fold closer than three days: the activity feed's file lists are read out of recent commits, and a prune that left one commit standing would take "what did that sync actually do" away with it.
