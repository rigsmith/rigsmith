---
"github.com/rigsmith/rigsmith": patch
---

Velopack: the macOS install DMG now opens as a proper "drag to Applications" window — a background backdrop with an arrow, the app icon on the left and the Applications folder on the right. A generic backdrop ships built into the adapter (used by default), and apps can override it with their own (e.g. branded) art via `macos.dmgBackground` (+ `macos.dmgWindow` for the logical window size of a HiDPI/2× image).

Also fixes the layout silently reverting to a default icon grid: the Finder-scripting step now targets the volume by the name it actually mounted under (a prior copy of the dmg still open no longer makes it script the wrong window), brings Finder to the front so icon positions commit, and unmounts cleanly after the positions settle so they survive into the compressed image. And it detaches any stale same-name volume before building, so the dmg mounts under its canonical name — the background-picture alias Finder records is tied to the build-time mount name, so a duplicate name would otherwise produce a dmg whose background fails to render on a user's clean mount.
