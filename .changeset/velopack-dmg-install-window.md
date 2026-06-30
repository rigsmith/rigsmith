---
"github.com/rigsmith/rigsmith": patch
---

Velopack: the macOS install DMG now opens as a proper "drag to Applications" window (backdrop + arrow, app icon beside the Applications folder), and the mounted volume carries the app icon. The layout is written deterministically by building the `.DS_Store` directly instead of driving Finder, so builds are reproducible and work headless / in CI. Apps can override the built-in backdrop via `macos.dmgBackground` (and `macos.dmgWindow` for a HiDPI image's logical size).
