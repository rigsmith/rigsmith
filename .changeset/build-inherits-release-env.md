---
"github.com/rigsmith/rigsmith": patch
---

The release `build` step now inherits the run's resolved environment (the layered `.env` / `.env.local` under the ambient shell), the same one the shell, `sign`, and forge steps already use. Previously native build adapters shelled out with only the bare process environment plus the `signing.env` config, so a secret kept in `.env.local` reached `gh` but not a desktop packager — you had to `source` it or duplicate it under `signing.env`. Now a desktop signer (Velopack / Tauri / Electron) sees it straight from `.env.local` — e.g. `AZURE_*` reaches `vpk` with no extra step. Implemented via a new `ArtifactsRequest.Env` (exposed to adapters as `BaseEnv()`); `signing.env` remains the masked path for real secrets.
