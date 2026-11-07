---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

Add stack edge pack reconstruction and edge status for carried fixes under republished NuGet ids

Reconstruct the committed manifest and fork integration snapshot, record explicit pack outcomes and provenance, and keep dry-run conflicts read-only. Canonicalize reconstruction and working-tree paths so NuGet retains project dependencies and original hooks chain reliably.

Preserve unrelated output files and directories during package cleanup, protect Git metadata paths, and reject non-edge members before fetching.
