---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig release` no longer builds unsigned when signing is configured and a signing secret can't be read. On a terminal it used to warn and carry on unsigned; it now stops, as it already did in CI, naming the secret (never its value) and how to supply it — `doppler login`, or the missing name. Pass the new `--unsigned` flag to build unsigned on purpose. With `--unsigned`, and on a `--dry-build`, nothing signs: a Velopack app skips its own macOS and Windows signing, electron-builder runs with its documented switches to skip signing and notarization, and Tauri builds with `--no-sign` (which also skips the updater signature from tauri-cli 2.9.5). `--unsigned` reads no signing secret at all; a `--dry-build` still reads them, to report any it can't, but passes none to the build.