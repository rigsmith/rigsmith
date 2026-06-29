# Velopack desktop release — non-.NET base (Rust)

Velopack is not .NET-only: `vpk pack` wraps any directory of built binaries, so
the rigsmith velopack adapter overlays **dotnet, cargo, node, and go**. A
`velopack.json` / `velopack.jsonc` beside a `Cargo.toml`, `package.json`, or
`go.mod` is released as a self-updating desktop app just like one beside a
`.csproj` — discovery and version stamping are delegated to that base ecosystem.

- [`velopack.jsonc`](./velopack.jsonc) — a Rust app (`base: "cargo"`) with a
  `build.command` that produces each channel's binaries for `vpk pack`.

See the [.NET example](../velopack-desktop/) for the matching `release.jsonc`
pipeline and signing details — those are identical regardless of base.

## How a non-dotnet base differs from .NET

| | .NET base | cargo / node / go base |
|---|---|---|
| Discovery / version source | the `.csproj` (via the dotnet adapter) | the `Cargo.toml` / `package.json` / `go.mod` (via that adapter) |
| Producing the pack directory | automatic `dotnet publish` | **`build.command`** (required) fills `$OUTPUT` |

The `build.command` runs once per channel through the platform shell with these
variables exported, so it needs no RID parsing:

| Variable | Example (`win-x64`) |
|---|---|
| `RID` / `CHANNEL` | `win-x64` |
| `OUTPUT` | absolute dir the command must fill (vpk then packs it) |
| `VERSION` | the resolved release version |
| `RUST_TARGET` | `x86_64-pc-windows-msvc` |
| `GOOS` / `GOARCH` | `windows` / `amd64` |

Set `build.packDir` when the build emits binaries somewhere other than `$OUTPUT`
(e.g. an electron-builder `out/<channel>` tree).
