---
"github.com/rigsmith/rigsmith": minor
---

Velopack: a new `macos.plist` config key feeds a custom `Info.plist` to `vpk pack --plist`, for bundle keys vpk doesn't generate (`NSServices`, `CFBundleURLTypes`, …). Because `--plist` replaces vpk's generated plist verbatim — no `CFBundleVersion` injection, and it can't be combined with `--bundleId` — the adapter renders `${version}` in the file to the release version before packing (so `CFBundleVersion` still tracks releases) and drops `--bundleId` automatically (the plist supplies `CFBundleIdentifier`). `--icon` still applies. Back-compat: omitting `macos.plist` keeps the current generated-plist + `--bundleId` behavior.
