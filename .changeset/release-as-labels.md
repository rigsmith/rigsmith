---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A version you pick with `--release-as`, or at the version prompt, is now labelled by the jump it actually makes. Forcing a patch change to 2.0.0 shows as `major` and gets a Major Changes heading, instead of `patch`. `status` doesn't take `--release-as`, so it still shows the planned bump.
