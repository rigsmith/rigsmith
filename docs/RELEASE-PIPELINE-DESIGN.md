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
  `.changeset/release.jsonc`. `DefaultOrder = [version, commit, publish, push, githubRelease]`.
- `githubRelease` (native step, `forge`) creates one GitHub release per package
  via `gh release create` — **notes only, no binary assets**.
- Go adapter is tag-native (version = latest matching git tag; `// rigsmith:version`
  comment is the untagged fallback). The real release op (tag create+push) is the
  `publish`/`push` steps.
- `shiprig release --dry-run` **prints the plan and executes nothing**
  (`run.go:127`) — useful to preview, useless to rehearse a build.
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
  Snapshot = tagless build for rehearse).
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
  anything ships. New native step inserted **before `publish`**:
  `[version, commit, **build**, publish, push, githubRelease]`.
- **attach** (upload the `Attach:true` artifacts to the forge release) needs the
  release to exist, so it is **folded into the `release`/`githubRelease` step** —
  not a separate trailing step.

**This supersedes the trailing-`artifacts` order in this doc and in
RELEASE-STEPS-AND-FORGES-DESIGN.md** (`… → release → artifacts`). When that doc's
`tag` promotion + `release` rename land, the chain is
`version → commit → build → publish → tag → push → release(+attach)`.

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

- **`release`/`githubRelease` owns the release + notes** (changelog → release body)
  **and the attach** (`gh release upload <tag> <Attach:true files> --clobber`).
- The Go builder runs `goreleaser release --skip=publish,validate` — it only
  *builds* into `dist/`; it never creates the GitHub release (shiprig owns that),
  so there is no double-create and no `release.mode: append` dance.

### 1c. `--rehearse` (real local dry-build, distinct from `--dry-run`)

- `--dry-run` stays plan-only. Add **`shiprig release --rehearse`**: runs the
  pipeline but forces every *mutating* step into a safe variant and **publishes
  nothing**:
  - `publish`/`push`/`githubRelease` → skipped (reported as "rehearsed").
  - `artifacts` → builder runs in snapshot mode (goreleaser `release --snapshot
    --clean`): builds all binaries into `dist/`, uploads nothing.
- Mechanism: the pipeline exports a signal to steps/hooks — env `SHIPRIG_REHEARSE=1`
  **and** a `${rehearse}` interpolation token so a custom `run` can branch
  (`goreleaser release ${rehearse:+--snapshot} --clean`). Built-in steps read the
  flag directly.

### 1d. `init` scaffolding + token preflight

- `shiprig init` (and `changerig init`) detect ecosystem + `.goreleaser.yaml` and
  scaffold `.changeset/release.jsonc` with the `artifacts` step wired, and ensure
  `.goreleaser.yaml` has `release.mode: append`.
- The `artifacts` step preflights its builder's needs (e.g. `GITHUB_TOKEN` for
  goreleaser) and fails early with a clear message.

## Part 2 — changerig manual changelog

### 2a. Changelog-only changeset — `changerig add --note`

- `changerig add --note "<text>"` writes a changeset that **names no packages**
  (no version bump) but **renders into the changelog/release notes** at `version`
  time, under a dedicated **"Notes"** section.
- Distinct from `--empty` (which names no packages *and* renders nowhere — kept as
  the "force a release PR / placeholder" device).
- Rendering: the planner/changelog renderer emits `--note` changesets against the
  release currently being cut (for single-version repos like rigsmith, that's the
  one release; for multi-package, a repo-level Notes block). Stays fully
  declarative — lives in `.changeset/`, flows through `version`, in git.

### 2b. Direct CHANGELOG.md prepend — `changerig changelog add`

- `changerig changelog add [package] -m "<entry>" [--type feat|fix|…] [--version X]`
  prepends a hand-authored entry straight into the package's `CHANGELOG.md` **now**,
  outside the changeset→version cycle.
- For: backfilling pre-tool history, corrections, or notes the generator can't
  produce. Respects the existing changelog format (reuses `core/changelog` writer
  + `core/mdfmt`). Idempotence is best-effort (it prepends; the human owns dedupe).
- `--version` targets an existing release heading (default: the unreleased/top
  section); no package arg in a single-package repo.

## Build slices (independent, in order)

1. **`Artifacts` plugin capability (1a)** — ✅ DONE: `plugin.Ecosystem` method +
   all five adapters (node/dotnet/cargo/go/regex) + `SubprocessEcosystem` + tests.
2. **`build` step early + `attach` in `release` (1a-next + 1b)** — new native
   `build` step before `publish` (`resolve.go` DefaultOrder + nativeBuiltins;
   `cli/release.go` handler calls `Artifacts()` per package into `dist/`); the
   `forge`/`githubRelease` step uploads each package's `Attach:true` files
   (`gh release upload --clobber`). Go builder injects `GORELEASER_CURRENT_TAG`
   so it is tag-order-independent.
3. **shiprig `--rehearse` (1c)** — `release.go` flag + `pipeline/run.go` signal
   plumbing (sets `ArtifactsRequest.Snapshot`) + built-in step branches.
4. **changerig changelog (2a + 2b)** — `changerig add --note`, a new
   `changerig changelog` command, `core/changeset` + `core/changelog`/`planner`.
5. **init scaffolding + preflight + install.sh/goreleaser cleanups (1d)**.

Each slice ships as its own PR off a worktree, with tests, leaving `go test ./...`
green.

## Open questions / risks

- **goreleaser append vs own**: default is append (shiprig owns notes). Confirm
  this reads well in practice on the first real release.
- **`--note` placement in multi-package repos**: repo-level "Notes" vs attaching
  to a chosen package — start repo-level; revisit if users want targeting.
- **rehearse fidelity**: snapshot binaries are unsigned/untagged-version; good for
  "does it build + package", not for verifying the published version string.
