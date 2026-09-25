---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A changeset synthesized from a commit takes git's unique abbreviation of the commit as its ID (at least 7 characters, longer where 7 is ambiguous) instead of a fixed 7-character prefix. Two commits in one release sharing those 7 characters used to share an ID, and with it the commit, pull request and author a changelog shows and the contributors a release credits.
