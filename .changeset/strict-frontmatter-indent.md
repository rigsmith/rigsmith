---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Changeset frontmatter follows YAML's indentation as @changesets reads it. Every package line has to start at the same column, however far indented, so `lib: patch` followed by ` quo: minor` is refused where it used to parse. A bump written on the lines below its package, indented deeper (`lib:` then `  patch`), is now read, where it used to be refused as a colon with no bump. Comment and blank lines can sit at any column.
