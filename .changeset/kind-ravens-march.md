---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig publish --dry-run` no longer waits on the registry one package at a time.

A dry run asks each registry whether a version is already published — that is what lets it report `already published` rather than `would publish` — and it asked serially, so the wait grew with the number of packages. A release generating 41 npm wrapper packages took 29 seconds at 27% CPU to say what it would do; it now takes under 6.

The probes run concurrently, bounded so a large workspace cannot open a connection per package. The report is unchanged, including its order: it reads in workspace order, not the order the answers arrive, so a dry run still reads exactly like the publish it previews. A real publish is untouched — still strictly sequential, still stopping at the first failure rather than racing more uploads out.