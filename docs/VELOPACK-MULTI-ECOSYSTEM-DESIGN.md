# Velopack multi-ecosystem adapter — design

Status: **implemented** — merged in #165 (follow-up to #164). See "Status" below.
Author: design captured 2026-06-29.

## Problem

Velopack is not a .NET-only packager. `vpk` wraps *any* folder of built binaries
into signed, self-updating installers + per-channel feeds — Velopack ships first-
class support for C#/F#, C++, **Rust, Go, and JS/Electron**. The `dotnet publish`
→ `vpk pack` flow is the convenient on-ramp, not a requirement; the generic path
is "build your app to a directory yourself, then `vpk pack --packDir <dir>`".

Our adapter currently hard-binds to .NET in three places (everything else is
already ecosystem-agnostic):

| Concern | Today | Generic? |
|---|---|---|
| `vpk pack`, `[win]`/`[osx]`/`[linux]` cross directives, host-aware Windows signing, DMG wrap, channels-as-RIDs, feed/Attach collection | in `pack.go` / `collect.go` | ✅ already generic — **no change** |
| **Discovery anchor** | `Discover` delegates to dotnet, keeps dirs with a `velopack.json` *next to a `.csproj`* | ❌ requires `.csproj` |
| **Version source** | `SetVersion` writes the `.csproj`/`Directory.Build.props` | ❌ dotnet-only |
| **Build step** | `Artifacts()` runs `dotnet publish -r <rid>` to make the packDir | ❌ dotnet-only |

The whole generalization reduces to one abstraction: **"give me a directory of
built binaries for RID X."** Everything downstream of that is already generic.

A concrete non-.NET consumer (Rust/Go/Electron) is on the near horizon, so we
design for it now and implement it as a focused follow-up to #164.

## Key finding: the overlay framework already supports this

The worry was that velopack would need to be *both* an overlay (when a base
manifest exists) and a standalone (generic, no base) — and that the framework
couldn't express that. Reading `internal/changerig/commands/workspace.go`
(`Discover` + `reconcileOverlays`), **it already can, with zero framework
changes**:

- Each adapter's own `Detect`/`Discover` runs independently. velopack's packages
  are produced by *velopack's* `Discover`, not by the base.
- `reconcileOverlays` only *drops a base package* when an overlay declares
  `Overlays: [...]` and claims the same `(baseID, dir)`. It is purely a
  de-duplication pass.
- So if velopack declares `Overlays: ["dotnet","cargo","node","go"]` and its own
  `Discover` finds a `velopack.json` dir, the matching base package (whichever of
  the four also discovered that dir) is dropped and velopack owns the unit — for
  **every** base, automatically.
- For a truly base-less generic app (`base: "none"`, no sibling manifest), no
  base discovers the dir, nothing is dropped, and velopack simply stands alone.
- Bonus we get for free: `reconcileOverlays` already **transfers the dropped
  base's intra-repo dependency edges** to the overlay when the overlay computed
  none — so the version cascade stays intact without velopack re-deriving deps.

That collapses the risk. The work is entirely inside the velopack package.

## Config additions (`velopack.json` / `velopack.jsonc`)

```jsonc
{
  "packId": "MyApp",
  "channels": ["osx-arm64", "osx-x64", "win-x64", "linux-x64"],

  // NEW — base ecosystem. Optional; auto-detected from the sibling manifest when
  // omitted: .csproj→dotnet, Cargo.toml→cargo, package.json→node, go.mod→go.
  // "none" = a base-less generic app (version lives here; see `version`).
  "base": "cargo",

  // NEW — how to produce the packDir vpk consumes. Optional for base=="dotnet"
  // (defaults to `dotnet publish`); required for cargo/node/go/none unless the
  // base grows a native publish-to-dir path later.
  "build": {
    // A shell command run once per channel, with these variables EXPORTED into its
    // environment (the shell expands $VAR — no string substitution by the adapter):
    //   RID/CHANNEL   the channel RID (e.g. win-x64)
    //   OUTPUT        the dir vpk will pack from — the command MUST fill this
    //   VERSION       the resolved release version
    //   RUST_TARGET   RID→cargo triple (win-x64→x86_64-pc-windows-msvc, ...)
    //   GOOS/GOARCH   RID→Go env (win-x64→windows/amd64, ...)
    "command": "scripts/pack-rust.sh",          // shell command string

    // Optional: if the build emits to a fixed dir instead of $OUTPUT, point vpk
    // at it (electron-builder's out/MyApp-win32-x64, etc.). Defaults to $OUTPUT.
    // Env-expanded too, so it may reference $CHANNEL/$OUTPUT/...
    "packDir": "out/$CHANNEL"
  },

  // DEFERRED (not yet implemented) — version source for base=="none" only.
  // "version": "0.1.0",

  // unchanged: icon, output, macos{}, windows{ signTemplate, trustedSigning }
}
```

Back-compat: an existing dotnet `velopack.json` with none of the new fields
behaves exactly as today (`base` auto-detects `dotnet`, no `build.command`, version
from the `.csproj`).

## Adapter changes (`core/ecosystem/velopack/`)

### Base resolution (implemented: embed the bases — no DI needed)

The design originally proposed injecting a registry resolver to avoid importing
every base adapter. That turned out unnecessary: `cargo`, `node`, and `gomod` do
**not** import `velopack` (and `dotnet` was already embedded), so velopack can
import and embed all four directly with no cycle. This keeps `New()`'s signature
unchanged, so `core/ecosystem/registry.go` needs **no** edit.

```go
type Adapter struct{ bases map[string]plugin.Ecosystem }

func New() *Adapter {
    a := &Adapter{bases: map[string]plugin.Ecosystem{}}
    for _, e := range []plugin.Ecosystem{dotnet.New(), cargo.New(), node.New(), gomod.New()} {
        a.bases[e.Info().ID] = e // gomod reports "go"
    }
    return a
}
```

### `Info`

```go
Overlays: []string{"dotnet", "cargo", "node", "go"},
```

(plus the existing `ManifestPatterns`, capabilities — unchanged).

### `Discover` (implemented: base-driven, generalizes today's dotnet filter)

Rather than a fresh tree walk, velopack asks each embedded base (in a deterministic
order) to discover its projects and keeps the dirs that carry a velopack file —
exactly today's dotnet flow, generalized to four bases:

```
for baseID in [dotnet, cargo, node, go]:
    for pkg in bases[baseID].Discover(req):
        if seen[pkg.Dir] or no velopack file in pkg.Dir: continue
        if cfg.Base != "" and cfg.Base != baseID: continue  // pin disambiguates
        keep pkg (carries the base's Name/Version/VersionFile/Dependencies)
```

Emit under ecoID `velopack`; reconciliation drops the base's duplicate. A dir has a
single manifest type, so order only fixes the rare double-manifest case (and the
`base` pin overrides it).

### `SetVersion` (implemented)

Resolve the base for the package's dir (`base` pin, else `detectBase`) and delegate
— the base rewrites `.csproj`/`Directory.Build.props`, `Cargo.toml`, `package.json`,
or the go version comment, format-preserving. (gomod delegation answers the
open question below — a Go-backed app's version is stamped by gomod.)

### `Artifacts` (`pack.go`)

Replace the hard-coded `dotnet publish` with a `producePackDir(cfg, ch, version,
env)` step:

- `base=="dotnet"` and no `build.command` → today's `dotnet publish -r <rid>`
  (unchanged fast path).
- otherwise → run `build.command` with the substitutions above under
  `req.BaseEnv()`, then `packDir = resolve(cfg.Build.PackDir or $OUTPUT)`.

Everything after — `vpk pack --packDir <packDir>`, the `[win]` directive,
host-aware signing, DMG wrap, feed collection — is unchanged.

## What explicitly does NOT change

- #164's host-aware Windows signing, `$VAR` expansion, storepass redaction.
- `collect.go` Attach classification (the updater feed).
- The plugin protocol (`core/plugin/protocol.go`) — no new fields needed.
- The overlay reconciliation in `workspace.go` — no framework change.

## Deferred to a follow-up

1. **Base-less apps (`base: "none"`).** Not implemented. It needs a separate tree
   walk to enumerate manifest-less velopack dirs plus a `version` field read/write
   in the velopack file. The four manifest-backed bases cover the real consumers
   (Rust/Go/Electron all ship a manifest), so `none` is deferred; an explicit
   `base: "none"` fails config validation today (clear signal it's unsupported).
2. **Native publish-to-dir per base.** v1 uses the `build.command` escape hatch for
   cargo/node/go. A later nicety: teach those base adapters a "publish
   self-contained to dir for RID" so `build.command` becomes optional (like dotnet).

## Resolved during implementation

- **No DI.** Embedding the four base adapters (none import velopack) beat injecting
  a registry resolver — `New()` and `registry.go` are unchanged.
- **Go version source.** `SetVersion` delegates to gomod, which stamps the version
  the Go way — no `base:"none"` fallback needed for Go.
- **RID coverage.** `ridGo` / `ridRustTarget` (in `pack.go`) map win/osx/linux ×
  x64/arm64/x86 to GOOS/GOARCH and Rust triples.

## Status

1. #164 (Windows signing) — **merged**.
2. This design — **implemented** on `feat/velopack-multi-ecosystem` (changeset:
   minor; no breaking change to existing dotnet `velopack.json`).
3. Docs/examples — `site/shiprig/` velopack page + a non-.NET `examples/` sample
   land in the same PR.
