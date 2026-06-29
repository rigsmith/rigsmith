---
"github.com/rigsmith/rigsmith": minor
---

Velopack packaging is no longer .NET-only. `vpk pack` wraps any directory of built binaries, so the adapter now overlays **dotnet, cargo, node, and go** — a `velopack.json`/`velopack.jsonc` beside a `.csproj`, `Cargo.toml`, `package.json`, or `go.mod` is released as a self-updating desktop app, with discovery and version stamping delegated to that base ecosystem.

- **`base`** (optional) — pins the base ecosystem; auto-detected from the sibling manifest when omitted.
- **`build`** — `build.command` produces the per-channel directory `vpk pack` wraps (required for cargo/node/go; the dotnet base still builds automatically via `dotnet publish`). The command runs through the platform shell with `CHANNEL`/`RID`, `OUTPUT` (the dir to fill), `VERSION`, `GOOS`/`GOARCH`, and `RUST_TARGET` exported so a `go build` or `cargo build --target` needs no RID parsing. `build.packDir` points vpk at the build's output when it lands elsewhere (e.g. an electron-builder `out/` tree).

Existing dotnet `velopack.json` files are unaffected (no `base`/`build` ⇒ the previous behavior). Base-less apps (`base: "none"`) are not yet supported. See `docs/VELOPACK-MULTI-ECOSYSTEM-DESIGN.md`.
