---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

In a changelog entry with typed changes, the released dependencies get a 🌊 Dependencies section of their own, one bullet per dependency, after the typed sections. A package with only 🚀 Enhancements and a dependency bump no longer shows a 🩹 Fixes section holding just "Updated dependencies". An entry with no typed changes keeps @changesets' layout.
