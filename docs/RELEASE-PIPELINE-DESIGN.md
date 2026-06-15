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

### 1a-next. The `artifacts` pipeline step (slice 2)

- New built-in step **`artifacts`** in `DefaultOrder` **after `push`** and after
  `githubRelease`: calls `Artifacts()` for each discovered package's ecosystem,
  collects `dist/`, then uploads the `Attach:true` artifacts to the release.
  `[version, commit, publish, push, githubRelease, artifacts]`.

### 1b. Release ownership (no double-create)

- **`githubRelease` owns the release + notes** (changelog → release body).
- **`artifacts`/goreleaser only attaches assets** → goreleaser runs with
  `release.mode: append` (documented + scaffolded into `.goreleaser.yaml`).
- Escape hatch: set `steps.githubRelease.enabled = false` to let goreleaser own
  the whole release (then it creates it). Default keeps shiprig owning notes.

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
2. **shiprig `artifacts` pipeline step + ownership (1a-next + 1b)** — calls
   `Artifacts()` per package, collects `dist/`, uploads `Attach:true` assets;
   `pipeline/resolve.go`, `run.go`, default order; goreleaser owns nothing (just
   builds with `--skip=publish`).
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
