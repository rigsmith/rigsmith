---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The window's Windows downloads are now spelled `claudeRigUi`, the same as its macOS one.

The two halves of the same release disagreed: `claudeRigUi_0.2.0_darwin_universal.zip` beside `clauderigUi_0.2.0_windows_amd64.zip`. The macOS bundle takes its name from the wordmark and the Windows binary took its from GoReleaser's `binary:` field, and nothing ever compared them. Neither was broken — each consumer referenced the right one — but a download URL written by hand from the other platform's example was always going to 404.

The Windows executable is `claudeRigUi.exe` now, which is also what its version resources declare and what a winget submission will use as its command alias. Windows resolves commands case-insensitively, so anything already typed keeps working. The 0.2.0 assets keep their old names; this takes effect from the next release.