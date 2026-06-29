---
"github.com/rigsmith/rigsmith": minor
---

Velopack: host-agnostic Windows signing, a real install DMG, and legible failures.

- **Azure Trusted Signing now works from any host with no hand-written `signTemplate`.** When cross-compiling a Windows build from macOS/Linux, the adapter mints a Trusted Signing token from the `AZURE_*` service-principal creds in the build env and synthesizes the `jsign` command itself (RFC3161 timestamp + `--signExclude '\.dll$'` baked in). On Windows it still uses vpk's native `--azureTrustedSignFile`. A pre-set `AZURE_CODESIGN_TOKEN` is honored, and an explicit `signTemplate` still overrides. Missing creds now fail fast naming exactly which `AZURE_*` variable is absent, instead of an opaque signer error.
- **macOS DMG is now a proper installer window** — the `.app` staged next to an `/Applications` symlink, arranged in icon view (drag-to-install), with a plain-symlink DMG fallback when Finder scripting is unavailable.
- **The `version` step no longer fails for a project in a subdirectory.** The changerig version writer now populates `Package.Dir`, and the Velopack overlay falls back to the manifest's directory when `Dir` is empty — previously it resolved the base ecosystem at the repo root and errored.
- **`0.0.0` no longer breaks `--dry-build`/`--only build`:** a skipped `version` step packs a valid `0.0.1` snapshot; a real build at `0.0.0` errors with guidance.
- **Failures are legible.** Command errors now include the tool's stdout (not just stderr) — vpk writes its fatal line to stdout, so errors that read `exit status 255:` now carry the real reason. The release TUI's failure panel surfaces the failing command's output instead of only `step 'X' failed`.
