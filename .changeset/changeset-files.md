---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Changeset files are now read the way Changesets reads them:
- you can single-quote package names, add comments and blank lines, and put the bump on the line below the package;
- mistakes that used to slip through are now errors naming the file: a package listed twice, a package with no bump, and a tab-indented or misaligned line.
