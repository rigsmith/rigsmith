---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig release --from <step>` no longer silently skips steps an unfinished release never ran. When a release stops partway, shiprig remembers the step it stopped at, and a later `--from` past it is refused with the steps it would skip: resuming "from publish" after a failure at `commit` used to publish packages `build` never built. Resume from the step shiprig names, or pass `--force` to skip them anyway. Every `--from` run now also lists the steps it skips.