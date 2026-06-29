---
"github.com/rigsmith/rigsmith": minor
---

Velopack packaging is no longer .NET-only — the adapter now overlays **dotnet, cargo, node, and go**, releasing a `velopack.json`/`.jsonc` beside any of their manifests as a self-updating desktop app. `base` pins the ecosystem (else auto-detected); `build.command` builds the pack directory for non-dotnet bases (dotnet still auto-runs `dotnet publish`). Existing dotnet configs are unchanged.
