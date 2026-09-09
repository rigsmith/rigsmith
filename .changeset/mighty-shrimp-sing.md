---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The claudeRig UI's Windows executable now carries an icon, a version and a description.

`build/winres/` had an entry for each CLI and none for the window, so `scripts/winres.sh` embedded nothing into it: a generic icon in Explorer, an empty properties dialog, and no FileDescription for winget's tooling to read — komac classifies a binary from exactly that field. It shipped that way for its whole life, and nothing in the repo said so.

Its version comes from `ui/go.mod` rather than from `git describe`, which would have answered with whichever tag is newest in the history — usually the CLIs' — and put a different number in the properties dialog than the app reports about itself.