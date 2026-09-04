---
type: feat
"github.com/rigsmith/rigsmith"
---

rig: a stack member can say it republishes its packages under a different id — `"publishesAs": { "Foo": "Acme.Foo" }`, or `"publishPrefix": "Acme."` for every id it produces. `stack wire` then redirects a reference to the republished id to the project producing the original, so an app written against your private feed still builds from source inside the stackspace instead of quietly taking the feed's copy. A rule naming a package the member does not produce is reported by `wire` and `doctor`.
