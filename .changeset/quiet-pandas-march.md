---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

The release action reports scoped packages it published. Its parser split a package coordinate at the first `@`, which a scoped name begins with, so `published @scope/pkg@1.2.3` matched nothing: those packages fell out of `publishedPackages` entirely, and a release publishing only scoped packages reported publishing none.