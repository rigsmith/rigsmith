---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

Ecosystem plugins gain a `published` method: whether a package's version is already on its registry, publishing nothing. The built-in npm, NuGet and crates.io adapters ask their registries; Go modules and the desktop adapters, released by their tag, answer that they have no registry. `shiprig publish-plan` asks it, so an external plugin needs to implement it, and list `published` in its info capabilities, before `publish-plan` runs in a repo that uses it.
