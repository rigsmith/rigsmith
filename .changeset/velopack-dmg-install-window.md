---
"github.com/rigsmith/rigsmith": patch
---

Velopack: the macOS install DMG now opens as a proper "drag to Applications" window — a background backdrop with an arrow, the app icon on the left and the Applications folder on the right. A generic backdrop ships built into the adapter (used by default), and apps can override it with their own (e.g. branded) art via `macos.dmgBackground` (+ `macos.dmgWindow` for the logical window size of a HiDPI/2× image).

The window layout is written **deterministically** — the adapter builds the `.DS_Store` itself (icon positions, window bounds, and the background-image alias) rather than driving Finder with AppleScript. This means dmg builds work **headless / in CI** (the AppleScript approach needs a live GUI session), are byte-for-byte reproducible, and don't suffer the Finder flakiness (focus stealing, layouts silently reverting to a default grid). The background alias is minted for the dmg's own volume name, so it resolves on any machine. The `.DS_Store` writer lives in a standalone, ecosystem-agnostic `core/dsstore` package (vendoring the MIT `gwend/dsstore` container with its `x/text` dependency dropped, plus a Go port of `mac_alias`) so other packaging plugins can reuse it.
