---
"github.com/rigsmith/rigsmith": patch
---

The release `build` step now inherits the run's resolved environment (`.env`/`.env.local` + ambient), so a desktop signer (Velopack/Tauri/Electron) gets secrets like `AZURE_*` straight from `.env.local` — no separate `source` or `signing.env` entry needed.
