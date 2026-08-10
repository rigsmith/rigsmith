---
type: fix
"github.com/rigsmith/rigsmith"
---

winget submissions go through komac again, so a version bump stops dropping half the package's metadata.

All five 1.5.1 submissions drew `Manifest-Metadata-Consistency`: `PublisherSupportUrl`, `Copyright`, `Tags`, `ReleaseNotes`, `ReleaseNotesUrl` and `Commands` had all vanished compared to the published 1.4.0 manifests, and `Moniker` had regressed from `changerig` to `ChangeRig`. The cause is structural rather than a missing setting — GoReleaser writes each manifest from its own config, so whatever it has no field for is gone, while komac updates the *published* manifest and rewrites only the version, URLs, hashes and notes. `Commands` — what `winget search` and `winget install --command` read — has no GoReleaser field at all.

`scripts/winget-submit.sh <version> [--submit]` now owns the lane: generate with komac, correct, verify, then submit. GoReleaser's winget publisher is disabled, its config kept so switching back is one line per package.

The correction step is not incidental. komac analyses each installer instead of trusting the published manifest, and reads `clauderig.exe` as an installer — writing `NestedInstallerType: exe` for that one package while getting the other four right. That is the same misdetection that shipped in ClaudeRig 1.4.0 and returned 23 days later as a moderator asking "Is this a Portable package?". It is corrected and logged before submission.

Because komac submits a directory as a separate step, `check-winget-manifests.sh` now runs **before** anything reaches winget-pkgs — a real gate, where every earlier version could only fire after the PRs were open. It also learned two things about manifests it had been assuming: the keys may sit at the root (komac) or inside each `Installers:` entry (GoReleaser), and winget-pkgs files are CRLF, which defeats `$`-anchored patterns. It now requires `Commands` too, so this regression cannot repeat silently. Fixtures captured from both producers, byte for byte, cover all four shapes.
