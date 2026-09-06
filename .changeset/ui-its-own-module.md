---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The window is its own module, at its own version.

It is a different product from the command line tools beside it — new, moving
fast, and at 0.x while they are at 1.x — and a single version line would either
hold it back or drag them forward. `ui/` is a Go module now, with `go.work`
tying it to the packages it imports, and the release stamps it from its own
version rather than from the tag.
