---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig device remove` now survives the next merge.

When two machines' registries diverged, the merge combined them by taking every row from both sides. That cannot distinguish "the other machine never had this row" from "this machine deleted it", so a removed device came back from the other side's untouched copy — on the next sync, and every sync after that.

The merge now reads the common ancestor: a row that one side dropped and the other left exactly as it was stays dropped, in either direction. A machine that has synced since the removal still comes back, because that one is a live machine again rather than the stale row you meant to clear out. With no common ancestor to compare against, both sides are kept as before — keeping too much is the safe way to be wrong about a registry.
