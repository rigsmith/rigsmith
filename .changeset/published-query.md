---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

Ecosystem plugins gain a `published` method: whether a package's version is already on its registry, publishing nothing. The built-in npm, NuGet and crates.io adapters ask their registries; Go modules and the desktop adapters, released by their tag, answer that they have no registry. An external plugin should implement it before `shiprig publish-plan` (coming next) is run in a repo that uses it.
