---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The automatic history squash was discarding history it never needed to, and saying nothing about it.

`sync` squashes when `.git` outgrows the working tree by `squashFactor`. It measured `.git` as it found it — and every sync writes its objects **loose and undeltified**. Transcripts are append-only, so each version is nearly identical to the last and packs down to almost nothing, but only once packed. On a real repo here `.git` measured 2.93 GB of which **2.39 GB was loose**; the actual history was 559 MB, and GitHub reported the same repo as 562 MB. The squash fired on the inflated figure, collapsed a month of history to a single commit, and force-pushed it — buying nothing that `git gc` would not have given back for free.

Sync now repacks first and re-measures before deciding. On that repo the threshold was 3.24 GB and a repack lands `.git` near 0.9 GB, so the squash simply does not fire.

When it does fire it no longer collapses everything. It keeps whole days — 30 by default, `retention.squashKeepDays` — and cuts on a **day boundary** rather than at the moment the threshold happened to trip. The old behaviour left the root commit at whatever o'clock the sync ran, so a repo reported that its history began at 08:18 on a Tuesday, with the first kept commit eleven minutes later. "Everything before this date" is what the cut is supposed to mean.

If a month is not enough relief, nothing further is folded and the ratio stays visible in `clauderig repo` and the window. That is deliberate: a tool that keeps eating history until the number looks acceptable is how a month went missing without anyone being asked.

`clauderig repo gc` runs the repack on demand, and both `repo prune` and the window's prune dialog point at it when most of `.git` turns out to be unpacked rather than old — refusing to trade history for something free.

Existing repos cannot recover what earlier squashes discarded; this stops it happening again.
