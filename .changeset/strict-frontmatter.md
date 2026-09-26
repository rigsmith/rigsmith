---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A changeset's frontmatter refuses what @changesets' YAML parser refuses. A package listed twice (`lib: patch` then `lib: minor`) used to release the last bump; a colon with nothing after it (`lib:`) released nothing, leaving the changeset stranded behind "Changesets found, but nothing to release"; and a tab-indented line was read as if it were not indented. Each is now an error naming the changeset and the package or line. A bare package line with no colon still takes its bump from the type.
