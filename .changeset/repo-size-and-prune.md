---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig repo` reports what the sync repo costs, and `clauderig repo prune` makes it smaller.

The staging repo is the one part of clauderig that grows without anyone deciding it should. Transcripts are append-only and sync every few minutes, so each run stores another copy of a file that only got longer: history outgrows the content it is history *of*, steadily and invisibly, and the first sign is usually a push getting slow. There has been a size-triggered squash for a while, but it fires on a ratio nobody can see and answers a question nobody got to ask.

`clauderig repo` prints the checkout size and file count, the size and number of commits behind it, the ratio between them, and the span of history still reachable. The ratio is the number worth watching and the one nobody works out by eye — on a real repo here it reads `1.62 GB across 3,391 files` against `2.91 GB in 285 commits`, or 1.8× the content.

`clauderig repo prune --before 7d` folds every commit older than that into a single base commit and keeps the ones after it, unchanged — same trees, same messages, same authors and dates. **The checkout does not change**: the files stay exactly as they are and only the record of how they got there is dropped. It rewrites the branch and force-pushes, which is what the automatic squash has always done, so other machines pick the new history up on their next sync.

The kept commits are rebuilt with `commit-tree` rather than replayed with `rebase`. Each carries its exact tree across, so nothing is diffed, there is no conflict to resolve, and a replayed commit cannot end up with a tree that differs from the one it recorded. It also costs time proportional to the number of commits rather than to their content, which matters when a week of syncing is a couple of thousand of them.

Rewriting history other machines share is interactive-only: it confirms in a terminal and refuses outright without one, with no flag to route around that. What it costs you is stated up front — sessions that retention deleted before the cutoff can no longer be recovered by `clauderig ledger backfill`, which reads them out of exactly the history this drops.

The window grows a **Repository** panel carrying the same numbers, with the ratio flagged amber once history costs four times the content, and a prune dialog offering a week, a month or three. It will not fold closer than three days: the activity feed's file lists are read out of recent commits, and a prune that left one commit standing would take "what did that sync actually do" away with it.
