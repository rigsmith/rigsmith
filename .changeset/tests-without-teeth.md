---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Six guards that had no working test now have one, found by breaking each one on purpose and seeing whether the suite noticed.

None of this changes what clauderig does. It changes what would be caught if someone changed it by accident. The ones worth knowing about: a Desktop-only sync could have started deleting sidecars, `restore --dir` could have started writing roots you never named, and the gate that refuses a public backup repo had tests for URL parsing and nothing else.

`cmd/clauderig/README.md` now describes the two ways a test ends up unable to fail, and how to sweep for them.
