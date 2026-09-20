---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

A transcript rewritten to the same size reaches the repo again.

The sync engine treats a file as unchanged when its mtime and size both match the staged copy's, which is safe only for an mtime that identifies the contents — so it holds that mtime against a measurement of how finely the filesystem records time, and restages anything written close enough to the last run to have shared a tick with it.

That measurement was wrong wherever the clock is finer than the loop taking it. It writes a file eight times and calls the smallest gap it sees the tick, so on an APFS Mac it reports 110µs one run and 220µs the next — for a filesystem that records nanoseconds. What it measures there is its own pace.

Under-stating the tick is the dangerous direction: it narrows the window and trusts an mtime that says nothing about the bytes behind it, and a transcript rewritten to the same size then never reaches the repo. Over-stating it only restages a file that did not need it. So a measurement finer than any kernel tick — 1ms to 10ms, by configuration — is no longer believed.

Found on Linux CI, where the kernel's coarse clock makes it reachable and where stat-ing a file between writes can persuade a recent kernel to stamp it finely, so the act of measuring changed the answer.

codexrig's engine carries the same probe and the same floor. It had no test over it at all; it has two now.
