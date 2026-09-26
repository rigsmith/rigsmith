---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`version --only` now fails if you name a package that's ignored or private, and says why. It used to print "Nothing to version." and succeed, which could let a release job pass without releasing anything.
