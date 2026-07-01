# The release pipeline

`shiprig release` runs a configurable step pipeline defined in
`.changeset/release.jsonc` — the orchestration layer on top of `version`,
`tag`, and `publish`. It's the Go port of net-changesets' release orchestrator.

```sh
shiprig release
shiprig release --dry-run        # preview the interpolated plan; nothing ships
shiprig release --dry-build      # build artifacts locally, publish nothing
shiprig release --local          # run the whole pipeline, but skip every network step
shiprig release --only build,publish   # run just these steps
shiprig release --from publish   # resume at a step after a failure
shiprig release --yes            # approve every confirm gate (CI)
```

## Built-in steps

With no `order` configured, the pipeline runs these steps in order. Any step can
be reordered, disabled, replaced, or have custom steps slotted between them.

| Step | What it does |
|------|--------------|
| `version` | Bump versions + write `CHANGELOG.md` (the shared engine) |
| `commit` | Commit the version/changelog changes (message via `message`) |
| `build` | Build release artifacts — runs early as a packaging preflight so a broken build fails before anything ships |
| `sign` | Code-sign built artifacts in place (desktop ecosystems) |
| `publish` | Push to the native registries (idempotent, registry-aware) |
| `tag` | Create the git tags for the released versions |
| `push` | Push commits + tags to the remote |
| `release` | Create the forge (GitHub/GitLab/Gitea) release + upload assets (idempotent) |
| `issues` | Comment on / close issues referenced by released commits (GitHub today) |

`version`, `commit`, `publish`, `tag`, and `push` are command-based (they shell
out to `tool`, default `shiprig`); `build`, `sign`, `release`, and `issues` are
native handlers.

## `.changeset/release.jsonc`

The pipeline file is JSONC (comments + trailing commas welcome). Top-level keys:

```jsonc
{
  "tool": "shiprig",        // command backing the built-in steps (e.g. "npx changeset")
  "shell": "portable",      // "portable" (default, in-process, cross-OS) or "system"
  "order": ["version", "build", "publish", "tag", "push", "release"],
  "vars": { /* … */ },      // reusable / captured / computed values
  "hooks": { /* … */ },     // before / after / onError around the whole run
  "steps": { /* … */ },     // per-step overrides keyed by step name
}
```

### Per-step configuration

Each entry under `steps` overrides one step:

| Key | Meaning |
|-----|---------|
| `enabled` | `true` / `false` / omit (use default) |
| `if` | A Tengo expression gating the step at runtime — falsy skips it (with a reason) |
| `ecosystems` | Restrict the step to these ecosystems (`node`, `dotnet`, `go`, `rust`, `tauri`, `electron`, `velopack`); skipped if the release has none of them |
| `name` | Display name shown in the plan + output |
| `run` | Custom command(s) — a shell string, an argv array, or a mix |
| `script` | A [Tengo script](#tengo-scripting) action (instead of `run`) |
| `args` | Extra args appended to a built-in step's command (e.g. `["--otp", "${vars.otp}"]`) |
| `message` | Commit message for the `commit` step (default `chore: release`) |
| `paths` | Scope the `commit` step to these paths (`git add -- <paths>`) instead of `git add -A` — keeps unrelated working-tree changes out of the release commit |
| `confirm` | `true` (default prompt), a custom prompt string, or `false` (no gate) |
| `dryRun` | Per-step dry-run behavior: `true` executes in dry-run, `false` hides it, or a command/array to run instead |
| `before` / `after` | Command(s) to run around the step's action |
| `forge` / `forgeURL` | Forge selection for the `release` step ([see below](#forge-releases)) |

A step name that isn't a built-in becomes a **custom step** — add it to `order`
and give it a `run` or `script`.

## Variables

`${...}` placeholders interpolate into commands, hooks, and other vars. Two
families:

**Built-in release variables** (populated from the release plan):

| Variable | Value |
|----------|-------|
| `${version}` / `${tag}` / `${changelog}` | Single-package shortcuts. `${version}` is the **new** (bumped) version for this release |
| `${lastVersion}` / `${nextVersion}` | The pre-bump version, and an explicit alias of `${version}` (for steps that reference both) |
| `${version.<key>}` / `${lastVersion.<key>}` / `${tag.<key>}` / `${changelog.<key>}` | Per-package, keyed by short package address |
| `${versions}` / `${lastVersions}` | Comma-separated `name@version` lists — the new versions, and the previous ones |
| `${tags}` | Array of tags |
| `${releaseUrl.<key>}` / `${releaseUrls}` | Forge release URLs (filled by the `release` step) |
| `${issues}` | Resolved issue numbers from released commits |
| `${env.NAME}` | The merged environment ([see `.env`](#environment-env)) |

`${version}` is the **bumped** version, computed from the pending changesets at
plan time — so it's correct in `--dry-run` and in every step, with no need to
read the new number back out of a manifest. `${lastVersion}` carries the version
the release is moving off (handy for a `v${lastVersion}...v${version}` compare
URL). These also appear on the script `ctx` (`ctx.version`, `ctx.lastVersion`,
`ctx.nextVersion`).

**User-defined `vars`** come in three forms:

```jsonc
"vars": {
  "basePath": "dist/pkg",                 // literal: reusable config, not masked
  "basePath2": { "value": "dist/pkg" },   // explicit literal form
  "otp": { "command": "op item get npm --otp", "lazy": true },  // captured: stdout, masked, lazy
  "channel": { "script": "ctx.version ? 'next' : 'latest'" },   // computed: Tengo expr
}
```

- **Literal** — a bare string or `{ "value": "…" }`. No side effects, not masked
  (it's config, not a secret) — ideal for paths reused across steps.
- **Captured** — `{ "command": "…" }`. The command's trimmed stdout becomes the
  value and is **masked** from logs. Add `"lazy": true` to defer it until first
  use (fresh, time-limited secrets like an OTP).
- **Computed** — `{ "script": "<tengo-expr>" }` evaluated over the script
  context. Not masked.

## Tengo scripting

A step's action (or a computed var, or an `if` gate) can be a
[Tengo](https://github.com/d5/tengo) script instead of a shell command — a small
embedded language that runs identically on every OS. Steps gate on `if`:

```jsonc
"steps": {
  "publish": { "if": "ctx.version" },     // skip when there's nothing to publish
  "notify": {
    "script": "sh(`echo released ` + ctx.tag); log('done')"
  }
}
```

Available modules/globals: `text`, `fmt`, `math`, `times`, `rand`, `json`,
`base64`, `hex`. Side-effecting helpers: `sh(cmd)`, `cp(...)`, `mv(...)`,
`rm(...)`, `mkdir(...)`, `log(msg)`, `fail(msg)`.

The script context `ctx` exposes `dryRun`, `env`, `packages`, `versions`,
`tags`, `issues`, and — for single-package releases — the scalars `ctx.version`,
`ctx.tag`, `ctx.changelog`.

## Cross-platform shell & file ops

`"shell": "portable"` (the default) runs shell-string commands through an
in-process interpreter, so `release.jsonc` behaves the same on Linux, macOS, and
Windows. `"shell": "system"` uses the OS shell instead. Either way, argv-array
commands run directly. The portable shell ships cross-platform builtins —
`cp` (`-r`/`-R`), `mv`, `rm` (`-r`/`-f`), `mkdir` (`-p`) — also reachable from
Tengo as `cp(...)` / `mv(...)` / `rm(...)` / `mkdir(...)`.

## Tag naming

By default the `tag` step names a tag per package:

- **Single-app repo** (exactly one package, non-Go) → `vX.Y.Z` — there's no
  sibling name to disambiguate, so the bare `v` form is used.
- **Multi-package repo** → `<name>@<version>` (the @changesets convention).
- **Go modules** → the module-path form `dir/vX.Y.Z` (or `vX.Y.Z` at the root),
  required for `go get`.

Override it for any repo with `tagTemplate` in the **changeset config** — honored
consistently by the `tag`, `publish`, and forge `release` steps and the `${tag}`
variable, so the release always attaches to the tag that was pushed:

```jsonc
// .changeset/config.json (or .changeset/shiprig.jsonc)
{
  "tagTemplate": "v${version}"          // or "${name}@${version}", etc.
}
```

Placeholders: `${version}` and `${name}`.

## Forge releases

The `release` step creates a release on your forge and uploads built assets.
Forge selection lives on that step:

```jsonc
"steps": {
  "release": {
    "forge": "auto",       // auto (detect from origin) | github | gitlab | gitea | none
    "forgeURL": ""         // base URL for self-hosted GitLab/Gitea
  }
}
```

`auto` detects GitHub.com → `github`, GitLab.com → `gitlab`, others need an
explicit value. `none` (or `--git-only`) degrades to tags only — the `issues`
step and forge URLs are skipped. Release creation and asset upload are
idempotent.

## Publish authentication

Per-ecosystem auth and OIDC trusted publishing are configured under each
ecosystem block in the release config:

```jsonc
"npm":    { "auth": "op://CI/npm/token" },          // 1Password secret reference
"cargo":  { "auth": "env:CARGO_REGISTRY_TOKEN" },   // an environment variable
"dotnet": { "auth": "cmd:op item get nuget --fields apikey", "oidc": "auto" }
```

- **`auth`** takes a secret reference: `op://vault/item/field` (1Password, via
  `op read`), `env:NAME`, or `cmd:…` (a command's stdout). Resolved secrets are
  masked from logs.
- **`oidc`** is `"auto"` (use OIDC trusted publishing when a CI OIDC context is
  present) or `"off"` (force a token). Supported for npm, crates.io, and
  NuGet.org.

Precedence per registry: an explicit `auth` ref wins; otherwise OIDC when a CI
context is present and not turned off; otherwise the ambient environment. See the
[publish-auth guide](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/PUBLISH-AUTH-GUIDE.md)
for the full matrix.

## Signing (desktop ecosystems)

Tauri and Electron releases can be code-signed via a `signing` block: build-time
signing through `signing.env` (e.g. macOS `CSC_*` / `APPLE_*` for
electron-builder / Tauri) and post-build signers under `signing.signers` (Azure
Trusted Signing for `.exe`/`.msi`, `rcodesign`/`codesign` for `.dmg`/`.app`).
Artifacts are signed in place by the `sign` step before `release` attaches them.
`--dry-build` previews the signer commands without contacting a signing service.
(Velopack apps sign differently — at `vpk pack` time, configured in
`velopack.json`; see [Desktop apps with Velopack](#desktop-apps-with-velopack).)

### Where the release config lives

shiprig resolves the pipeline file from **one** of these locations. If more than
one exists it stops and lists them rather than guessing (a `.json` + `.jsonc`
pair counts as two); with none, the built-in defaults run.

- `.changeset/release.jsonc` · `.changeset/release.json`
- `.changeset/shiprig.jsonc` · `.changeset/shiprig.json`
- `release.jsonc` · `release.json` · `shiprig.jsonc` · `shiprig.json` (repo root)
- a `"shiprig"` (or `"release"`) key inside `.rig.json`:

```jsonc
// .rig.json
{
  "shiprig": {
    "order": ["version", "build", "publish", "tag", "push", "release"]
    // …the same keys as a standalone release config
  }
}
```

- a `"release"` key inside the **changeset config** file
  (`.changeset/config.json` / `changerig.jsonc`):

```jsonc
// .changeset/config.json — one file for both tools
{
  "versioning": { "source": "commits" },
  "ignore": [],
  "release": {
    "order": ["version", "publish", "tag", "release"]
    // …the pipeline; changerig ignores this key
  }
}
```

`shiprig release --config <file>` overrides discovery with an explicit path.

### One file for both tools

shiprig is a superset of changerig, so you can keep **both** configs in a single
file instead of two — it's optional, and existing two-file setups keep working
unchanged. It goes both ways:

- changeset config at the top level **+ a `release` key** (a `config.json`, as
  above), or
- the pipeline at the top level **+ a `changeset` key** (a `shiprig.jsonc`):

```jsonc
// .changeset/shiprig.jsonc
{
  "$schema": "https://rigsmith.dev/schemas/shiprig.json",
  "order": ["version", "publish", "tag", "release"],
  "changeset": { "versioning": { "source": "commits" }, "ignore": [] }
}
```

Both tools read whichever file you choose. If you end up with two files,
`shiprig doctor` flags it and offers to **merge them into one
`.changeset/shiprig.jsonc`**. Defining the *same* config in two places is a loud
error (shiprig never guesses).

## Desktop apps with Velopack

A desktop app packaged with [Velopack](https://velopack.io) is a first-class
release unit. Drop a `velopack.json` (or `velopack.jsonc`) next to the project's
manifest and the `build` step produces each channel's binaries + `vpk pack`s them,
wraps the notarized macOS `.app` in a `.dmg`, and the `release` step attaches the
installers **and the self-update feed** to the forge release — no `pack.sh`, no
`vpk upload`.

**Velopack is not .NET-only** — `vpk pack` wraps any directory of built binaries,
so the adapter overlays **dotnet, cargo, node, and go**. The base ecosystem is
auto-detected from the sibling manifest (`.csproj` / `Cargo.toml` / `package.json`
/ `go.mod`); version bumps and the changelog are delegated to it, so they work
exactly as for any project in that language. For a **dotnet** base the build runs
`dotnet publish --self-contained` automatically; **every other base supplies a
`build.command`** (see below).

```jsonc
// velopack.json — next to the .csproj
{
  "packId": "MyApp",
  "channels": ["osx-arm64", "osx-x64", "win-x64"],   // each RID = one update feed
  "mainExe": "MyApp",                                  // ".exe" added for Windows
  "icon": { "macos": "app/icon.icns", "windows": "app/icon.ico" },
  "macos":   { "bundleId": "com.acme.myapp",
               "signIdentity": "Developer ID Application: …",
               "notaryProfile": "myapp-notary" },      // xcrun notarytool profile
  "windows": {
    // cross-compiling from macOS/Linux: a custom signer (jsign), {{file}} per binary
    "signTemplate": "jsign --storetype TRUSTEDSIGNING --keystore … --storepass $TOKEN --alias acct/profile {{file}}",
    // building natively on Windows: vpk's native Azure Trusted Signing
    "trustedSigning": { "endpoint": "…", "account": "…", "profile": "…" }
  }
}
```

- **Channels are RIDs.** Each is a per-architecture self-update feed the app
  subscribes to. macOS channels build only on a macOS host; Windows/Linux
  cross-build anywhere — vpk gets the `[win]` / `[linux]` cross directive
  automatically. `--dry-build` packs everything **unsigned** for a fast local
  rehearsal.
- **Non-dotnet bases use `build.command`.** cargo/node/go have no built-in
  publish-to-directory step, so describe the build. It runs once per channel
  through the shell with `RID`/`CHANNEL`, `OUTPUT` (the absolute dir to fill, which
  vpk then packs), `VERSION`, `RUST_TARGET`, and `GOOS`/`GOARCH` exported — so a
  `cargo build --target $RUST_TARGET` or `go build` needs no RID parsing. Set
  `build.packDir` when the build emits elsewhere (e.g. electron-builder's `out/`).
  Optionally pin `base` to override auto-detection.

  ```jsonc
  // velopack.jsonc — next to a Cargo.toml (Rust app)
  { "packId": "MyRustApp", "base": "cargo", "channels": ["win-x64", "osx-arm64"],
    "build": { "command": "cargo build --release --target $RUST_TARGET && mkdir -p \"$OUTPUT\" && cp target/$RUST_TARGET/release/myapp* \"$OUTPUT\"/" } }
  ```
- **Signing is build-time**, inside `vpk pack` (not the `sign` step). The
  non-secret identifiers live in `velopack.json`; the secrets (the macOS `.p12`
  password, the signing token) come from the [signing
  env](#signing-desktop-ecosystems) (masked) or simply from `.env.local` — the
  build step inherits the run's [environment](#environment-env). **Windows signing
  is host-aware**: cross-compiling from macOS/Linux uses `windows.signTemplate`
  (e.g. jsign), while a native Windows build uses `windows.trustedSigning` (vpk's
  Azure Trusted Signing) — set whichever matches where you build, the adapter
  picks by host. `$VAR`/`${VAR}` in a `signTemplate` are expanded from the build
  env (vpk runs it without a shell), so `--storepass $AZURE_CODESIGN_TOKEN` works
  from a pre-set env var; the token is redacted from any echoed command.
- **Updates need no `vpk upload`.** Velopack's in-app updater finds updates by
  listing a release's assets over the GitHub API — the `releases.<channel>.json`
  index `vpk pack` produces plus the `.nupkg` payloads — so attaching those to the
  forge release is a complete, working feed.

The result is a fully native desktop release — `version → commit → build → tag →
push → release` with no packaging or upload scripts.

## Environment & `.env`

Before running, `shiprig release` loads `.env` and `.env.local` from the repo
root and layers them **under** the ambient shell environment (`.env` <
`.env.local` < exported variables — a real `export` always wins). That merged
environment is what every part of the run sees:

- `${env.NAME}` placeholders in steps, hooks, and vars resolve from it;
- the commands each step runs (publish, tag, push) inherit it;
- the native `build` and `sign` steps inherit it too, so a desktop packager
  (Velopack / Tauri / Electron) sees your `.env` secrets — e.g. `AZURE_*` for a
  Windows signer reaches `vpk` straight from `.env.local`, no separate sourcing;
- forge releases run with it, so `gh` finds its token;
- `shiprig init`'s token preflight checks it, so a token kept in a local `.env`
  reads as ✓ set rather than a false ⚠.

This means a release token can live in `.env.local` (git-ignored) instead of
being exported in every shell. The `.env` files themselves are read, never
written or printed.

Secret masking only redacts values it has been given — the ones captured through
`vars` or resolved from `auth` references. A value interpolated straight into a
command with `${env.NAME}` is **not** automatically masked, so avoid putting a
raw secret on a command line that gets logged.

Pass `--no-env` to drop the `.env`/`.env.local` layer for a run (the ambient
shell environment still flows through) — handy when a stray local `.env` would
otherwise shadow what you've exported.

## Local rehearsal: `--dry-run`, `--dry-build`, `--local`, `--rehearse`

Four flags keep a release on your machine, trading off how much of the pipeline
actually runs:

- `--dry-run` interpolates and prints the full plan but executes nothing, except
  steps explicitly marked `"dryRun": true` (or given a dry-run command).
- `--dry-build` runs **only** the `build` step to produce artifacts locally (a
  snapshot), then stops — it publishes, tags, and pushes nothing. Global hooks
  and captured vars are dropped so it can't trigger OTP prompts. Requires an
  enabled `build` step.
- `--local` runs the **whole pipeline for real** but skips every step that
  reaches the internet — `publish`, `push`, `release`, and `issues`. The version
  bump, commit, build, sign, and local `tag` all execute, so it exercises the
  full release and produces real artifacts while nothing leaves the machine. Use
  it to confirm an end-to-end release works before letting it ship.
- `--rehearse` is `--local` that *also* skips the git `commit` and `tag`, so
  version, build, and sign run for real but nothing is committed, tagged,
  pushed, or published. It touches neither git history nor the network, so a
  release can be dry-run end to end and re-run at will. (The longhand equivalent
  is `--local --skip commit,tag`.)

```sh
shiprig release --local            # full pipeline, nothing pushed/published
shiprig release --local --from build   # resume a local rehearsal at a step
shiprig release --rehearse         # like --local, and leave git untouched too
```

`--local` is a real run, so it can't combine with the plan-only `--dry-run` or
the build-only `--dry-build`. It *does* compose with `--only`/`--skip`/`--from`/
`--to`, so a local rehearsal can be narrowed or resumed; the network steps stay
skipped regardless. (The longhand equivalent is
`--skip publish,push,release,issues`.) `--rehearse` composes the same way and is
likewise mutually exclusive with `--dry-build`.

::: tip Implementation
The pipeline lives in `internal/shiprig/pipeline` + `internal/shiprig/forge`;
see the [feature-parity audit](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/FEATURE-PARITY.md)
for the delivered surface.
:::
