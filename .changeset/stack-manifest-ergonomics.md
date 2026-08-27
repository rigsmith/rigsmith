---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

A stack manifest now takes a repository URL wherever it takes a repo spec. Pasting the URL from your browser, the one the clone button hands out, or an ssh remote used to be rejected outright, which was pure friction — the information was all there, in the form you happened to have it in. Rig reduces any of them to the canonical `host/owner/name` on load, so `https://github.com/acme/some-lib.git` and `git@github.com:acme/some-lib.git` both work.

A freshly scaffolded `rig.stack.jsonc` also validates cleanly now. The schema demanded at least one entry under `repos`, so the file `rig stack init` writes for you was marked invalid the moment it was written, and the documented next step — fill it in and run init again — began with your editor showing an error. Rig still refuses to import an empty manifest, with a message that says what to do instead.
