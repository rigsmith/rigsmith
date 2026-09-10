---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

A failing package no longer ends a `--all` run. Every package still runs, the closing line counts them (`✓ 33 ok  ✗ 1 failed  – 1 skipped`), and the error names each one — so a single CI log shows every broken package instead of only the first. The live dashboard already worked this way; the plain path now matches it, so a run reports the same thing whether or not stdout is a terminal.