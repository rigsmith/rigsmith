---
"github.com/rigsmith/rigsmith": patch
---

Fix Velopack Windows packaging when cross-compiling from macOS/Linux: the adapter now prepends vpk's `[win]` directive and signs via a new host-aware `windows.signTemplate` (native Windows still uses `windows.trustedSigning`). `$VAR`s in the template expand from the build env, and `--storepass` tokens are redacted from echoed commands.
