---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Changelogs that use conventional types (🚀 Enhancements, 🩹 Fixes, and so on) no longer mix in Minor Changes or Patch Changes headings: an untyped change joins the typed section for its bump (🚀 Enhancements for a minor, 🩹 Fixes for a patch) when your config has one, and dependency updates get their own 🌊 Dependencies section. Changelogs without types look the same as before.
