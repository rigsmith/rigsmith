---
type: feat
scope: rig
"github.com/rigsmith/rigsmith/core"
---

Go modules in a stackspace now resolve each other from the tree. A `require` on a sibling module goes to the proxy however close its sources are — being next to it changes nothing — so `rig stack wire` writes a `go.work` listing every module in the tree, and `rig stack doctor` reports when a hand-written one is missing a module and a require on it is therefore being fetched rather than read.

The generated file declares the highest language version any of the modules asks for. Leaving that line out is not an option: a `go.work` without it is implicitly Go 1.18 and refuses to load any module that wants more, which is a confusing error a long way from its cause.

Discovery gained a matching option rather than changing what it reports. Asking for a package's registry-referenced siblings is now something a caller opts into, so the release path sees exactly what it saw before and nothing about which packages cascade has changed.
