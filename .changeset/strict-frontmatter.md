---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A changeset's frontmatter refuses what @changesets' YAML parser refuses. A package listed twice (`lib: patch` then `lib: minor`) used to release the last bump; a colon with nothing after it (`lib:`) released nothing, leaving the changeset stranded behind "Changesets found, but nothing to release"; and a tab-indented line was read as if it were not indented. Each is now an error naming the changeset and the package or line. A package line with no bump (`"lib"`) still takes its bump from the type, and with no type it is now refused too, as @changesets refuses it, instead of releasing nothing; `add --bump auto` without a type writes `"lib": none` so its file still reads.
