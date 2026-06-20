---
planStatus:
  planId: plan-tauri-electron-support
  title: Tauri & Electron support for shiprig
  status: completed
  planType: feature
  priority: medium
  owner: john
  stakeholders: []
  tags: [shiprig, ecosystem, desktop, release]
  created: "2026-06-15"
  updated: "2026-06-15T00:00:00.000Z"
  progress: 100
---

> **Merged** 2026-06-15 as `c7003c6 …(#101)` (squash). All CI green incl.
> windows-latest; verified on `origin/main`. Branch + worktree cleaned up.

# Tauri & Electron support for shiprig

## Objective

Let shiprig version and release **desktop apps** built with Tauri and Electron —
the two frameworks whose underlying ecosystems (Rust/Cargo and Node) shiprig
already handles. Neither is a registry-published package; both are distributed as
**native installers attached to a forge release** (GitHub/GitLab/Gitea), often
with code-signing and an auto-updater manifest.

## Key insight: two axes + a claim mechanism

shiprig's `plugin.Ecosystem` contract (`core/plugin/ecosystem.go`) has six methods
that cluster into two independent jobs:

| Axis | Methods | Job |
|---|---|---|
| **Versioning** | `Discover`, `SetVersion` | Where the version lives; read/write it + intra-repo dep edges |
| **Distribution** | `Publish`, `Artifacts`, `ReleaseInit` | How to ship — registry push **or** build-installers-and-attach-to-forge-release |

npm/cargo/NuGet use the same tool for both axes. Go and `regex` are file-versioned
+ tag-distributed. **Tauri and Electron each reuse an existing versioning axis but
need the "build native installers → attach to forge release" distribution axis**,
which the `Artifacts` method + `Attach: true` + the existing forge-release step
already provide a seam for.

**Both are first-class adapters** (decision, see below — symmetry with how the
Tauri adapter claims its crate from cargo, the Electron adapter claims its
`package.json` from node):

- **Tauri adapter** (`core/ecosystem/tauri`) — owns `tauri.conf.json`. Cargo-shaped
  (file stamp + no-op-by-tag publish) **plus a `tauri build` step** in `Artifacts`.
- **Electron adapter** (`core/ecosystem/electron`) — owns an Electron
  `package.json` (electron dep / builder config). Versions `package.json` (reusing
  node's stamping helper), no-op-by-tag publish, `electron-builder`/`forge` in
  `Artifacts`.
- **A shared claim/skip mechanism** so the base language adapter (cargo/node)
  yields a package to its desktop overlay — implemented in the discovery
  coordinator, *without* teaching cargo/node about desktop frameworks.
- **Signing/notarization → a shared, OPTIONAL seam.** Off by default; builds
  succeed unsigned with a warning when signing config/secrets are absent.

No new pipeline steps. The pipeline (`version→commit→build→publish→tag→push→
release→issues`) already has build/publish/release; the framework builds land in
the **build/artifacts** stage and bundle upload lands in the existing **forge
release** stage.

## Decisions (from design review 2026-06-15)
1. **Tauri claims, cargo skips.** A crate sitting under a `tauri.conf.json` is
   owned by the tauri adapter and released as a desktop app, not a crate.
2. **Electron is its own adapter too** (not a node backend) — for consistency with
   #1. The same claim/skip resolution applies: electron claims its `package.json`,
   node skips it.
3. **Tauri version → Cargo.toml in lockstep.** When `tauri.conf.json` holds the
   version (not deferred), stamp `Cargo.toml` to match as well.
4. **Electron builder: auto-detect, prefer electron-builder.** forge config wins
   if present; else electron-builder config/`build` key; else default to
   electron-builder (it emits `latest.yml` for auto-update).
5. **Updater manifests: emit & attach if the builder produces one** (Tauri
   `latest.json`, electron `latest.yml`); don't generate/sign them ourselves in
   v1.

## Reference points in the existing code

- `core/plugin/ecosystem.go` — the `Ecosystem` interface; `Registry`/`DetectAll`.
- `core/ecosystem/registry.go` — `Default()` registers adapters; **order matters**
  (regex is last so language adapters claim files first).
- `core/ecosystem/cargo/cargo.go` — shape for Tauri (TOML hand-parse,
  format-preserving `SetVersion`, no-op-by-tag publish; `setPackageVersion`,
  `rewriteInTable` are reusable).
- `core/ecosystem/node/node.go` — shape for Electron (`package.json` parse,
  `setStringField` first-match splice, workspace globbing).
- `core/ecosystem/regex/regex.go` — config-block loading pattern
  (`config.Load` + `cfg.Ecosystem`).
- `core/plugin/protocol.go:200-297` — `ArtifactsRequest`/`Artifact`/`Attach`/
  `Snapshot`, and `ReleaseInit`: `TokenSpec` (advisory preflight, never blocks) +
  `BuildConfigSpec` (scaffold a build-config file).
- **To locate during impl:** the discovery coordinator where per-ecosystem
  `Discover` results are merged into the package set (planner / `core/discovery`).
  That is where the claim/skip reconciliation lands.

---

## Part 0 — Shared claim/skip mechanism — ✅ DONE (branch `feat/ecosystem-overlays`)

Implemented as `EcosystemInfo.Overlays []string` + reconciliation in
`Workspace.Discover` (`internal/changerig/commands/workspace.go`), the single
discovery point every changerig/shiprig verb routes through. An adapter declares
the base ids it overlays (`tauri`→`["cargo"]`, `electron`→`["node"]`); after all
adapters run, `reconcileOverlays` drops any base package whose `Dir` an overlay
also claimed. Order-independent, base adapters untouched. Before dropping a base
package it **transfers that package's intra-repo dependency edges to the claiming
overlay** (when the overlay computed none), so the version cascade still reaches
the desktop app (a workspace-lib bump still patch-bumps it). Covered by unit
tests (claims same dir / leaves different dir / inherits deps). `go build`/
`go vet`/full suite green. **Constraint for Parts 1–2:** the overlay
adapter must report the same `Package.Dir` as the base it claims (Tauri → the
crate dir, Electron → the package.json dir).

Both desktop adapters "overlay" a base language adapter for the same package, so
the base adapter must yield. Resolve this **once**, generically, rather than
teaching cargo/node about Tauri/Electron:

- Desktop adapters mark themselves as **overlay** ecosystems that claim specific
  manifests/dirs (e.g. via an `EcosystemInfo` flag or a small registry-side list:
  `tauri` claims the Cargo.toml in/under a `tauri.conf.json` dir; `electron`
  claims an Electron `package.json`).
- In the discovery coordinator, after every adapter's `Discover` runs, reconcile:
  for each package an overlay adapter claims (matched by directory or manifest
  path), **drop the base adapter's package for the same dir/manifest**. The
  overlay wins; the base adapter needs no code change.
- `DetectAll` consequences: a repo with only a desktop app reports the desktop
  ecosystem (its base adapter ends up with no packages post-reconcile); a polyglot
  repo with a library *and* a desktop app correctly reports both. Deterministic
  ordering: overlays reconcile against bases in a fixed pass.

This single mechanism serves Decisions #1 and #2 identically.

---

## Part 1 — Tauri adapter (`core/ecosystem/tauri`) — ✅ DONE

Implemented `core/ecosystem/tauri/tauri.go` (+ tests), registered in
`registry.go` (after cargo, before regex). `Overlays: ["cargo"]`; Capabilities
`Discover`/`SetVersion`/`Artifacts`/`ReleaseInit` (no Publish). Versioning handles
both modes — conf-sourced (semver in `tauri.conf.json`) stamps conf **and**
`Cargo.toml` in lockstep (Decision #3); cargo-sourced (empty/path/absent version)
stamps `Cargo.toml` only. `Package.Dir`/`Name` align with the cargo crate so
overlay reconciliation drops the duplicate and existing changesets keep
resolving. v1/v2 config shapes both read. `Artifacts` runs `cargo tauri build`,
finds the bundle dir (crate-local or shared workspace `target/release/bundle`),
and attaches installers + any `latest.json`/`.sig` (Decision #5). Full repo suite
green (59 pkgs; 60 after Part 2). Intra-repo cargo dependency edges are inherited
from the dropped cargo package via the Part 0 dep-transfer, so the cascade reaches
the app. **Known v1 limitation** (documented in code): only canonical
`tauri.conf.json` is parsed (not Tauri.toml / tauri.conf.json5).

## Part 1 — Tauri adapter (`core/ecosystem/tauri`) — design notes

Registered in `registry.go` before `regex`. Overlay over cargo (Part 0).

### Identity & detection
- `ID: "tauri"`, `DisplayName: "Tauri"`, `ManifestPatterns: ["tauri.conf.json"]`.
- `Detect`: a `tauri.conf.json` (also `Tauri.toml`/`tauri.conf.json5`) under root.
- Capabilities: `Discover`, `SetVersion`, `Artifacts`, `ReleaseInit` (Publish is a
  no-op skip, like Go/regex).

### Versioning — the `tauri.conf.json` ↔ `Cargo.toml` pair
- Read version from `tauri.conf.json`'s top-level `"version"`.
- **Deferral form** `"version": "../Cargo.toml"` → the crate is the source of
  truth; read/stamp `Cargo.toml` only (reuse cargo's `setPackageVersion`).
- **Own-version form** → stamp `tauri.conf.json` **and** `Cargo.toml` in lockstep
  (Decision #3).
- Format-preserving writes: JSON via a `setStringField`-style first-match splice;
  TOML via cargo's `rewriteInTable`.
- Frontend `package.json` sync is out of scope for v1 (use a `node`/`regex` entry).

### Distribution — `Artifacts` runs `tauri build`
- `Publish`: `{Skipped: true, Message: "released via git tag + forge release"}`.
- `Artifacts`: shell `tauri build` (`cargo tauri build`), collect bundles from
  `target/release/bundle/**` (`.dmg`, `.app.tar.gz`, `.msi`, NSIS `.exe`,
  `.AppImage`, `.deb`, `.rpm`) → `Artifact{Kind: ArtifactBinary|ArtifactArchive,
  Attach: true}`. If `tauri build` also emits a `latest.json`, attach it
  (Decision #5).
- Respect `DryRun` (report intent) and `Snapshot` (tagless rehearse build).

### `ReleaseInit`
- `BuildConfigSpec`: detect `tauri.conf.json`; offer a starter if missing; Tool
  `"tauri"`; PATH preflight for the Tauri CLI.
- `TokenSpec`s: **only** when signing is enabled (Part 3).

---

## Part 2 — Electron adapter (`core/ecosystem/electron`) — ✅ DONE

Implemented `core/ecosystem/electron/electron.go` (+ tests), registered in
`registry.go` (after node). `Overlays: ["node"]`; Capabilities `Discover`/
`SetVersion`/`Artifacts`/`ReleaseInit` (no Publish). Per-package Electron
detection: `electron` in deps/devDeps, a `build` key, an electron-forge config
(`config.forge` or `forge.config.*`), or an `electron-builder.*` file. `SetVersion`
stamps `package.json` `"version"` format-preservingly. `Artifacts` auto-selects
the builder (Decision #4: electron-forge when a forge config is present, else
electron-builder via `npx`), then collects installers from `out/make` / `dist`
(.dmg/.exe/.AppImage/.deb/.rpm/.snap/.nupkg/.zip/.blockmap) plus any `latest*.yml`
updater manifest (Decision #5). Intra-repo dep edges are inherited from node via
the Part 0 dep-transfer, so the cascade still reaches the app. `shiprig doctor`
now shows an info row for tauri/electron (forge-artifact build, no publish tool).
Full repo suite green (60 pkgs).

## Part 2 — Electron adapter (`core/ecosystem/electron`) — design notes

Registered before `node` (or reconciled as an overlay per Part 0). Versioning is
plain `package.json`; only distribution differs from a Node library.

### Identity & detection
- `ID: "electron"`, `DisplayName: "Electron"`,
  `ManifestPatterns: ["package.json"]`.
- `Detect`/claim is **per package**: a `package.json` is an Electron app when it
  has `electron` in deps/devDeps, **or** an `electron-builder`/`electron-forge`
  config exists (`electron-builder.{yml,yaml,json,js}`, a `build` key, or
  `forge.config.js`). A monorepo may have one Electron app among many libraries —
  only the matching package is claimed from node.
- Capabilities: `Discover`, `SetVersion`, `Artifacts`, `ReleaseInit`.

### Versioning
- `Discover`/`SetVersion` reuse node's package.json logic (`setStringField`,
  intra-repo dep edges). The version is `package.json` `"version"` — unchanged from
  how node handles it; the adapter just owns it so distribution differs.

### Distribution — `Artifacts` runs the builder
- `Publish`: no-op-by-tag (Electron apps are usually `private:true`; not
  `npm publish`'d). Distribution is the forge release.
- `Artifacts`: builder auto-detect (Decision #4) — `forge.config.js` →
  `electron-forge make`; else `electron-builder --publish never`; collect
  installers from `dist/`/`out/` (`.dmg`, NSIS `.exe`, `.AppImage`, `.deb`,
  `.snap`, `blockmap`, `latest*.yml`) as `Attach: true`. Attach `latest*.yml` if
  produced (Decision #5).
- Optional `electron-builder --publish always` (its own GitHub publisher) is
  config-gated and default off — we prefer shiprig's single forge-attach path.

### `ReleaseInit`
- `BuildConfigSpec`: detect a builder config; offer an electron-builder starter if
  none; Tool `"electron-builder"`/`"electron-forge"`; PATH preflight.
- `TokenSpec`s: only when signing is enabled (Part 3).

---

## Part 3 — Optional signing & notarization seam (shared) — ✅ DONE

Implemented with the engine-resolves approach (chosen 2026-06-15):
- **Protocol** (`core/plugin/protocol.go`): `ArtifactsRequest.Signing
  *SigningCreds` (additive, no APIVersion bump); `SigningCreds.Env` is a resolved
  ENV_VAR→value map.
- **Config** (`core/config`): `EcosystemConfig.Signing *SigningConfig`
  (`{enabled, env}`), absent/`enabled:false` by default. `env` maps the build
  tool's standard variables (CSC_*/APPLE_*/TAURI_SIGNING_*) to `op://`/`env:`/
  `cmd:` references — same forms as the per-ecosystem `auth` key.
- **Resolution** (`core/sign`): `ResolveEnv` resolves each ref through the
  existing `core/auth` precedence seam and masks every value; returns nil when
  unconfigured (unsigned build, zero secrets read).
- **Wiring** (`shiprig release` build step): `resolveSigning` reads the
  ecosystem's signing block, resolves + masks, and passes `Signing` into
  `Artifacts`. Adapters merge it into the build environment (`mergeSigningEnv`)
  so electron-builder/tauri sign automatically; the build message notes
  `(signed)`. Generic env-map design → no hardcoded per-platform matrix.

**v1 behavior choice:** signing disabled/absent ⇒ unsigned, no secrets (the
default, the key requirement). Signing enabled but a secret fails to resolve ⇒
the build errors (loud, predictable) rather than silently shipping unsigned. The
"warn + unsigned on a local TTY, error on CI" degrade from the original sketch is
a deliberate follow-up, not v1. Tests: `core/sign` (resolve/mask/missing-fails),
both adapters' `mergeSigningEnv` + signed dry-run.

### Original design sketch

**Signing is opt-in and never required.** The contract already supports it:
`TokenSpec`s are *advisory* — the wizard reports set/missing and warns but does not
block.

- A shared `signing` config sub-block per desktop ecosystem (`tauri.signing`,
  `electron.signing`), **absent/`enabled:false` by default**.
- **Disabled (default):** build **unsigned**, declare **no** signing `TokenSpec`s,
  emit a one-line note ("building unsigned; set `<eco>.signing` to enable"). Zero
  secrets — `dry-run` and local builds just work.
- **Enabled:** declare the platform's signing secrets as `TokenSpec`s (so
  `release init`/`doctor` preflight them) and pass them to the builder **via env,
  never argv** (mirrors cargo/npm token threading). Resolve through `core/auth` so
  `op://…`/`env:`/`cmd:` refs work, masked in output.
- **Graceful degrade:** enabled but secrets missing on a non-CI/TTY run → warn and
  build unsigned; on CI → error (signing was explicitly requested). Matches the
  `--yes`/TTY split used elsewhere.

### Platform secrets (declared only when enabled)
- **macOS (both):** `APPLE_CERTIFICATE`/`APPLE_CERTIFICATE_PASSWORD` (or keychain
  identity), notarization via `APPLE_ID`/`APPLE_PASSWORD`/`APPLE_TEAM_ID` or
  `APPLE_API_KEY`/`APPLE_API_ISSUER`.
- **Windows:** Authenticode cert + password, or an Azure Trusted Signing / cloud-HSM
  reference.
- **Tauri updater signing:** `TAURI_SIGNING_PRIVATE_KEY`(+`_PASSWORD`) when an
  updater manifest is in play.

A small shared helper (`core/sign`) maps `(framework, platform, signing-config)` →
env vars + builder flags so the two adapters don't duplicate the matrix.

---

## Out of scope (v1)
- Frontend `package.json` version sync for Tauri.
- We **attach** updater manifests but don't generate/sign them (Decision #5).
- Non-GitHub auto-update servers (S3, generic, Squirrel server).
- Mobile Tauri targets (iOS/Android).
- electron-builder multi-publisher fan-out.

## Follow-ups — ✅ DONE (2026-06-15, commit `a1c8eb8` on the same PR)
The four PR-#101 follow-ups landed:
- **Post-build `sign` step** (`build → sign → publish → … → release`) — a generic,
  **platform-agnostic** pass: `signing.signers` is a list matched by file
  extension, each signer either the `azure-trusted-signing` preset (dotnet `sign`
  CLI) or a `command` (`{file}` substituted). Windows `.exe`/`.msi`, macOS
  `.dmg`/`.app` (rcodesign/codesign), etc. all sign through the same step;
  `release` attaches the signed files. Creds resolved through `core/auth`, masked.
  No-op unless configured; previews in `--dry-build`. (`core/sign/signers.go` —
  `config.Signer`, `sign.Apply`/`SignerCommands`; `resolve.go` order; `release.go`
  signHandler.) Started Windows-only (Azure) then generalized per review. Covers
  the old .NET Authenticode note for free (any ecosystem's signable artifacts).
- **Signing-degrade refinement** — a secret that won't resolve degrades to an
  unsigned build with a warning on a TTY, hard error in CI (`resolveSigningEnv`).
- **Tauri config variants** — also parse `tauri.conf.json5` (via `jsonc`) and
  `Tauri.toml`, with lockstep stamping. (json5 unquoted keys still unsupported.)
- **`FEATURE-PARITY.md`** — intentionally **left untouched** on this branch (zero
  diff vs main). An earlier "Done since" note was reverted to avoid clashing with
  a separate uncommitted edit to that file on main's working tree; fold the
  desktop-ecosystem/sign-step note in once that edit lands.

### Still open (smaller)
- Build-time `env` signing (macOS `CSC_*`/`APPLE_*`, in-process notarize) stays a
  separate concern from the post-build `signers` pass; both can be used together.
- No built-in macOS notarization preset — macOS goes through a `command` signer
  (e.g. `rcodesign`); an `apple-notarize` preset could be added later.
- A dedicated `dotnet` desktop/exe artifact path (so `dotnet publish` `.exe`s flow
  through the sign step) is not built — the seam is ready for it.

## Rollout / sequencing
1. ✅ **Claim/skip mechanism** (Part 0) + dependency transfer.
2. ✅ **Tauri adapter** (mirrors cargo).
3. ✅ **Electron adapter** (mirrors node distribution).
4. ✅ **Optional signing seam** (Part 3) — Tauri + Electron, env-threaded.
5. ✅ **Updater attach** — `latest.json` (Tauri) / `latest*.yml` (Electron)
   attached when the builder emits them (in `collectBundles`/`collectInstallers`).
6. ⬜ **`FEATURE-PARITY.md` update** — deferred: there is a pre-existing
   uncommitted change to that file on `main`; updating it now risks clobbering
   that work / conflicting with the branch. Do it once that change settles (mark
   Tauri/Electron as built; they're the nominated next-candidate at line 286).

7. ✅ **Demos** — `examples/desktop-demo/{tauri,electron}`: minimal empty-window
   apps, each a self-contained shiprig workspace with a `run.sh` (build in a
   tempdir). Electron verified end-to-end (`shiprig release --dry-build` →
   electron-builder → `Shiprig Electron Demo-0.2.0-arm64.dmg`); Tauri verified
   through `version` (lockstep stamp of `tauri.conf.json` + `Cargo.toml`), build
   scripted for a Rust+tauri-cli host (cargo absent in this env).

**All code is on branch `feat/ecosystem-overlays`** (worktree
`/Users/john/Git/rigsmith-worktrees/feat-ecosystem-overlays`). `go build`,
`go vet`, and the full suite (61 packages) are green. One PR planned for the
whole feature; not yet pushed.

## Testing
- Claim/skip: a repo that is only-Tauri reports `tauri` not `cargo`; only-Electron
  reports `electron` not `node`; a polyglot repo with a library + a desktop app
  reports both, each package owned once.
- Tauri: fixtures for both version-source modes (own `version` vs. `../Cargo.toml`
  deferral); golden tests for lockstep stamps; a faked `tauri` binary for
  `Artifacts`.
- Electron: per-package detection matrix (electron dep / builder config / neither)
  in a polyglot workspace; faked `electron-builder`/`electron-forge`.
- Signing: disabled-path declares no tokens + builds unsigned; enabled-path threads
  masked secrets via env and preflights them; missing-secret degrade (warn on TTY,
  error on CI).
