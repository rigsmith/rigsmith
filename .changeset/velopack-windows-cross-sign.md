---
"github.com/rigsmith/rigsmith": patch
---

Fix Velopack Windows packaging when cross-compiling from macOS/Linux. The adapter ran plain `vpk pack` (so vpk stayed in host/osx mode and rejected the `.ico` and Windows flags) and emitted `--azureTrustedSignFile`, which vpk only exposes when running **on** Windows. Now Windows packaging is host-aware:

- **Cross-compiling from macOS/Linux** → prepend vpk's `[win]` directive and sign via a new `windows.signTemplate` config (a custom command — e.g. jsign + Azure Trusted Signing — run per binary with `{{file}}`), plus `--signExclude '\.dll$'` so only the `.exe`/`Setup.exe` are signed.
- **Building natively on Windows** → unchanged: `--azureTrustedSignFile` from `windows.trustedSigning`.

The cross directive (`[win]`/`[osx]`/`[linux]`) is added only when the channel targets a different OS than the host, so the native macOS path is untouched. A `--storepass` token in a `signTemplate` is redacted from any echoed command.
