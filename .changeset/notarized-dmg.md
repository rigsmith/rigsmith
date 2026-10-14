---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

A Velopack macOS `.dmg` is now notarized and stapled, not only signed, when `macos.notaryProfile` is set (with `macos.signIdentity`, which a notary profile now requires — on its own it skipped notarization silently). Before, only the `.app` inside was notarized, so Gatekeeper rejected the downloaded disk image as "Unnotarized Developer ID". Notarizing adds a few minutes per channel to a release build, and a failure to sign, notarize or staple the DMG now fails the build and removes the DMG instead of shipping it un-notarized. Nothing changes without a notary profile, or for `--dry-build`.
