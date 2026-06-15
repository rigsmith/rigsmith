# Release pipeline & manual changelog — design

> Status: **DESIGN / agreed scope** (2026-06-14). Drives implementation; build in
> the slices at the end. Decisions locked with John.

## Goals

1. **Seamless binary releases via shiprig** — `shiprig release` (run locally, by a
   human, on demand) versions → tags → creates the GitHub release → **and**
   builds + attaches cross-platform binaries. No CI auto-publish.
2. **General, not rigsmith-only** — rigsmith users span .NET / Node / Go / Rust.
   The binary-build step must be ecosystem-pluggable; goreleaser is the *default*
   for Go, not a hardcode.
3. **Rehearsable** — a human can dry-build the whole release locally (binaries and
   all) and publish nothing, before committing to a real run.
4. **Manual changelog control** in changerig — both declarative non-bumping notes
   and a direct CHANGELOG.md escape hatch.

## Current state (grounding)

- Pipeline engine (`internal/shiprig/pipeline`): configurable steps with per-step
  `before`/`after` hooks, custom steps, vars, confirm gates. Config:
  `.changeset/release.jsonc`. `DefaultOrder = [version, commit, publish, push, release]`.
- `release` (native step, `forge`) creates one GitHub release per package
  via `gh release create` — **notes only, no binary assets**.
- Go adapter is tag-native (version = latest matching git tag; `// rigsmith:version`
  comment is the untagged fallback). The real release op (tag create+push) is the
  `publish`/`push` steps.
- `shiprig release --dry-run` **prints the plan and executes nothing**
  (`run.go:127`) — useful to preview, useless for actually building anything.
- `changerig add` already has `-m` (summary→entry), `--open` ($EDITOR body),
  `--empty` (names no packages — and renders nowhere today).
- goreleaser builds the 4 binaries; `.goreleaser.yaml` validated. `scripts/install.sh`
  downloads goreleaser assets (missing `changerig` in its target list — fix).

## Part 1 — shiprig

### 1a. A cross-ecosystem `Artifacts` plugin capability — ✅ DONE (this PR)

The earlier "goreleaser-default-command step" idea was wrong-shaped: **every**
ecosystem has a build-the-distributable operation, and today it is missing (and
`publish` even discards what it builds — `dotnet pack` → temp dir → push → gone).
So artifacts is a **fourth release method on the plugin contract**, not a
goreleaser special case.

- New `Artifacts(ctx, ArtifactsRequest) (ArtifactsResponse, error)` on
  `plugin.Ecosystem` (method `"artifacts"`), mirrored by `SubprocessEcosystem`,
  implemented by every built-in adapter. Capability-gated via `EcosystemInfo`.
- Builds distributables into `req.OutputDir` and returns `[]Artifact{Path, Kind,
  Attach}`. **Decision: build to `dist/` always; attaching is opt-in** — encoded
  per-artifact by `Attach` (binaries/archives → `true`; registry packages →
  `false`). `Snapshot`/`DryRun` flags on the request (DryRun reports intent;
  Snapshot = tagless build for --dry-build).
- Per adapter:
  - **node** → `npm pack --pack-destination <out> --json` → `.tgz` (Attach:false)
  - **dotnet** → `dotnet pack -c Release -o <out>` → `.nupkg` (Attach:false)
  - **cargo** → `cargo package --no-verify --allow-dirty --target-dir <out>` → `.crate` (Attach:false)
  - **go** → goreleaser (`release --clean --skip=publish`, or `--snapshot`) when
    `.goreleaser.yaml` present → archives + checksums (Attach:true); else Skipped
  - **regex** → Skipped (no capability advertised)
- Pre-release, so the contract method was added directly — no gating/migration.
  Since `publish` no longer needs to re-build, a follow-up can have it reuse the
  `dist/` artifacts (e.g. dotnet pushes the retained `.nupkg`).

### 1a-next. `build` early + `attach` at release — NOT a trailing `artifacts` step (slice 2)

`artifacts` is really **two concerns** with opposite ordering needs, so it splits:

- **`build`** (produce `dist/`) has no dependencies and **should run early** — it
  doubles as a packaging *preflight*, so a broken build fails the release before
  anything ships. New native step inserted **before `publish`**.
- **attach** (upload the `Attach:true` artifacts to the forge release) needs the
  release to exist, so it is **folded into the `release` step** (the forge step) —
  not a separate trailing step.

**Implemented canonical order** (this supersedes the trailing-`artifacts` order in
both this doc and RELEASE-STEPS-AND-FORGES-DESIGN.md). The forges doc's `tag`
promotion + `githubRelease`→`release` rename (its slices 1a/1b) are **now done**,
so the chain is:

```
version → commit → build → publish → tag → push → release(+attach)
```

- `publish` narrows to the registry push (`--no-git-tag`); the new `tag` step
  (`<tool> tag`) creates the local tags; `push --follow-tags` puts them on the
  remote before `release`.
- `githubRelease` → `release` is a **clean rename, no alias** (pre-release).

**Parity across ecosystems** — every ecosystem now produces its distributable in
the *same* `build` step (no more Go-is-special):

| Ecosystem | `build` produces | shipped by | attached? |
|---|---|---|---|
| Go | goreleaser → archives + checksums | the tag (publish no-op) | yes |
| node | `npm pack` → `.tgz` | `npm publish` | no (opt-in) |
| .NET | `dotnet pack` → `.nupkg` | `nuget push` | no (opt-in) |
| Rust | `cargo package` → `.crate` | `cargo publish` | no (opt-in) |

**Order-independence (key):** `build` runs before any tag exists, so the builder
must get the version without reading a git tag. Manifest-versioned ecosystems
(npm/cargo/dotnet) already have the bumped version in their manifest; **Go** gets
it injected via `GORELEASER_CURRENT_TAG=v<version>` + `--skip=publish,validate`,
so goreleaser stamps the right version with no tag at HEAD.

- Follow-up (Layer B): have `publish` *reuse* `build`'s `dist/` output instead of
  re-packing (`npm publish <tgz>`, `nuget push <nupkg>`) — eliminates the double
  build. The step model above already gives the safety + parity; Layer B is the
  efficiency pass.

### 1b. Release ownership (no double-create)

- **`release`/`release` owns the release + notes** (changelog → release body)
  **and the attach** (`gh release upload <tag> <Attach:true files> --clobber`).
- The Go builder runs `goreleaser release --skip=publish,validate` — it only
  *builds* into `dist/`; it never creates the GitHub release (shiprig owns that),
  so there is no double-create and no `release.mode: append` dance.

### 1c. `--dry-build` (real local dry-build, distinct from `--dry-run`) — ✅ DONE

- `--dry-run` stays plan-only. **`shiprig release --dry-build`** is a *real* run that
  **executes only the `build` step** and skips everything else — so it builds the
  release's artifacts into `dist/` and **commits/tags/pushes/publishes nothing**.
- Implemented as `ResolveOptions.DryBuild`: every step except `build` resolves to
  `SkipReason: "dry-build: build only"`; the `build` handler passes
  `ArtifactsRequest.Snapshot: true` so the Go builder runs goreleaser
  `release --clean --snapshot` (no tag/version needed) and other adapters pack
  their current manifest. Routes through the sequential reporter (no plan-editor
  TUI / confirm gates — there's nothing to gate).
- Scope note: this builds *current-version snapshot* artifacts to verify "does it
  build + package", not the exact next-version strings (you'd run the real
  pipeline for that). The `${dry-build}` interpolation token / `SHIPRIG_DRY_BUILD`
  env for custom non-`build` builders is **not** implemented — under --dry-build only
  the `build` step runs, so a custom command step would be skipped; revisit if
  someone needs a custom builder under --dry-build.

### 1d. `init` scaffolding + token preflight — ✅ done

Driven by a new plugin method, `release-init` (`MethodReleaseInit`), so `init`
holds **no** per-ecosystem knowledge — each adapter declares its own release
prerequisites and the wizard loops over them. The contract is additive
(`APIVersion` stays 1):

```go
ReleaseInit(ctx, ReleaseInitRequest{RepoRoot, Packages}) (ReleaseInitResponse, error)
// ReleaseInitResponse{ Tokens []TokenSpec; BuildConfig *BuildConfigSpec; Notes []string }
```

- **`Tokens`** — env vars a real publish/attach needs (`NPM_TOKEN`,
  `NUGET_API_KEY`, `CARGO_REGISTRY_TOKEN`, `GITHUB_TOKEN`). The wizard reports
  set (✓) vs. missing (⚠ + what-for + where-to-get). It **never reads the value**
  — a preflight, not a credential store.
- **`BuildConfig`** — a config file the ecosystem needs to build artifacts. Only
  Go returns one (`.goreleaser.yaml`): it's the lone ecosystem with no native
  single-command artifact producer. The **gomod plugin** templates the starter
  from the `package main` dirs it discovers (one build+archive block per binary),
  so the goreleaser knowledge stays in the plugin, not in `init`. `Present: true`
  when a config already exists → reported and left untouched.
- **`Notes`** — short human lines (the publish target).

`shiprig init` composes: it runs the changeset scaffold (shared with
`changerig init`), then scaffolds `.changeset/release.jsonc` (a documented
starter whose `order` is the engine's own `DefaultOrder`, so it can't drift),
prompts to write any `BuildConfig` (auto-writes off a TTY), preflights the build
`Tool` on PATH, and prints the token checklist. `changerig init` stays
changesets-only. node/dotnet/cargo pack natively, so they return a token + note
and no build config; regex declares nothing and doesn't advertise the capability.

## Part 2 — changerig manual changelog

### 2a. Changelog-only changeset — `changerig add --note` — ⬜ TODO (own slice)

- `changerig add --note "<text>"` writes a changeset that **names no packages**
  (no version bump) but **renders into the changelog/release notes** at `version`
  time, under a dedicated **"Notes"** section.
- Distinct from `--empty` (which names no packages *and* renders nowhere — kept as
  the "force a release PR / placeholder" device).
- **Why this is its own slice (bigger than 2b):** it is a *format + planner +
  renderer* change, not a localized command. It needs (1) a changeset-format
  marker (`note:` frontmatter) + `core/changeset` parse/render; (2) the planner to
  collect note changesets (today a no-package changeset yields no `Module`); (3)
  the changelog generator/protocol (`ChangelogRequest`) to carry notes and render
  a "Notes" section; (4) the `version` flow + parity goldens. **Open design fork:**
  changelogs are *per-package* with no repo-level CHANGELOG — so where does a
  repo-level note land? Leading option: attach the note to **every package
  released** in the run (one package ⇒ rigsmith's case). Decide before building.

### 2b. Direct CHANGELOG.md prepend — `changerig changelog add` — ✅ DONE

- `changerig changelog add [package] -m "<entry>" [--type feat|fix|…] [--version X]`
  prepends a hand-authored entry straight into the package's `CHANGELOG.md` **now**,
  outside the changeset→version cycle. For backfilling history, corrections, or
  notes the generator can't produce. Reuses `core/changelog.WriteEntry` (newest on
  top, under the `# Title`).
- Implemented as a new `changelog` command group in `internal/changerig/commands`.
  The entry is a `## <version|Unreleased>` block + one bullet (`- msg`, or
  `- **type:** msg` with `--type`). Package arg optional in a single-package repo.
- **Simplification vs the original sketch:** it always *prepends a new section*
  (it does not merge a bullet into an existing `## <version>` heading). That's the
  honest escape-hatch behavior — the human owns dedupe/merge. Smart-merge into an
  existing heading is a possible future nicety.
- **Stays formatted:** `add` runs the changelog through the formatter after
  writing, and `changerig changelog format [package]` re-tidies a hand-edited
  `CHANGELOG.md`. Both use the configured `format` formatter (the same one the
  `version` step applies to released entries) and fall back to the built-in native
  markdown formatter when none is configured — so the file stays clean whether you
  use the command or edit by hand. (This was John's actual goal: easily add info +
  keep it formatted; the declarative 2a route wasn't needed.)

## Build slices (independent, in order)

1. **`Artifacts` plugin capability (1a)** — ✅ DONE: `plugin.Ecosystem` method +
   all five adapters (node/dotnet/cargo/go/regex) + `SubprocessEcosystem` + tests.
2. **`build` step early + `attach` in `release` (1a-next + 1b)** — new native
   `build` step before `publish` (`resolve.go` DefaultOrder + nativeBuiltins;
   `cli/release.go` handler calls `Artifacts()` per package into `dist/`); the
   `forge`/`release` step uploads each package's `Attach:true` files
   (`gh release upload --clobber`). Go builder injects `GORELEASER_CURRENT_TAG`
   so it is tag-order-independent.
3. **shiprig `--dry-build` (1c)** — `release.go` flag + `pipeline/run.go` signal
   plumbing (sets `ArtifactsRequest.Snapshot`) + built-in step branches.
4. **changerig changelog (2a + 2b)** — `changerig add --note`, a new
   `changerig changelog` command, `core/changeset` + `core/changelog`/`planner`.
5. **init scaffolding + preflight (1d)** — ✅ DONE: `release-init` plugin method +
   all five adapters + `SubprocessEcosystem` + the composed `shiprig init`
   (release.jsonc starter, goreleaser starter, token preflight) + tests. Remaining
   under this theme: `scripts/install.sh` is missing a `changerig` target.

Each slice ships as its own PR off a worktree, with tests, leaving `go test ./...`
green.

## Open questions / risks

- **goreleaser append vs own**: default is append (shiprig owns notes). Confirm
  this reads well in practice on the first real release.
- **`--note` placement in multi-package repos**: repo-level "Notes" vs attaching
  to a chosen package — start repo-level; revisit if users want targeting.
- **dry-build fidelity**: snapshot binaries are unsigned/untagged-version; good for
  "does it build + package", not for verifying the published version string.
