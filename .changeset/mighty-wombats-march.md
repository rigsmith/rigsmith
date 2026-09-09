---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The claudeRig UI now ships on its own tag, so a window release no longer waits for a toolchain release.

It has always been a separate module on its own version — 0.x while the command line tools are at 1.x — and `shiprig tag` has always rendered `ui/vX.Y.Z` for it. Nothing consumed that tag: the window rode the CLIs' release, which meant every fix to it waited for one, and the Windows download lived in a release named after a version the app does not have.

Pushing `ui/vX.Y.Z` now fires its own workflow, built from the pieces that already existed. GoReleaser builds and Authenticode-signs the Windows binaries through the same hook the CLIs use, a macOS runner builds, signs and notarizes the `.app` through the same script as before, and the two are published together as one GitHub release, a Homebrew cask and a winget submission. `brew install --cask rigsmith/tap/clauderig-ui` is unchanged; the zip it downloads now sits in a release carrying the window's own version rather than the CLIs'.

A tag that disagrees with `ui/go.mod` is refused before anything is built. The two are written at different moments, and a mismatch would ship a window reporting one number under a tag promising another, with the cask and the winget manifest each believing a different one.