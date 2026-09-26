---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A changeset's frontmatter is read as the YAML @changesets reads it: a package name can be single-quoted (`'@acme/lib': patch`, with `''` for a quote) or plain (`lib: patch`) as well as double-quoted, a bump can be quoted, and blank lines and `#` comments are allowed. Single-quoted names used to fail with "malformed frontmatter line", blocking every command in a repo that had one.
