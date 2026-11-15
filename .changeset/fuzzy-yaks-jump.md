---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig release` no longer builds unsigned when signing is configured and a signing secret can't be read. On a terminal it used to warn and carry on unsigned; it now stops, as it already did in CI, naming the secret (never its value) and how to supply it — `doppler login`, or the missing name. Pass the new `--unsigned` flag to build unsigned on purpose. With `--unsigned`, and on a `--dry-build`, nothing signs: no signing secret is read, a Velopack app skips its own macOS and Windows signing, electron-builder runs with its documented switch to skip signing and notarization, and Tauri builds with `--no-sign`.