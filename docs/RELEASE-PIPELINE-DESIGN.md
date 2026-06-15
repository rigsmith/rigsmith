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

### 1a. A generic `artifacts` build step (new built-in)

- New built-in step **`artifacts`**, added to `DefaultOrder` **after `push`**
  (the tag must exist before a builder can attach to it) and **after
  `githubRelease`** so the release exists to attach to:
  `[version, commit, publish, push, githubRelease, artifacts]`.
- It runs a **builder command** and attaches its outputs as release assets.
  Resolution of the default command (when `steps.artifacts.run` is unset):
  - Go + a `.goreleaser.yaml` present → `goreleaser release --clean`
  - (future) Rust + `Cargo.toml` + cargo-dist → `cargo dist build`
  - otherwise the step is **inert** (no-op + a one-line "no builder detected" note),
    so non-binary repos are unaffected.
- Fully overridable via `steps.artifacts.run` in `release.jsonc` (any command).

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

1. **changerig changelog (2a + 2b)** — most self-contained, immediately useful;
   touches `changerig add`, a new `changerig changelog` command, `core/changeset`
   (note marker) + `core/changelog`/`core/planner` (note rendering).
2. **shiprig `artifacts` step + ownership (1a + 1b)** — `pipeline/resolve.go`,
   `run.go`, default order; goreleaser `release.mode: append`.
3. **shiprig `--rehearse` (1c)** — `release.go` flag + `pipeline/run.go` signal
   plumbing + built-in step branches.
4. **init scaffolding + preflight + install.sh/goreleaser cleanups (1d)**.

Each slice ships as its own PR off a worktree, with tests, leaving `go test ./...`
green.

## Open questions / risks

- **goreleaser append vs own**: default is append (shiprig owns notes). Confirm
  this reads well in practice on the first real release.
- **`--note` placement in multi-package repos**: repo-level "Notes" vs attaching
  to a chosen package — start repo-level; revisit if users want targeting.
- **rehearse fidelity**: snapshot binaries are unsigned/untagged-version; good for
  "does it build + package", not for verifying the published version string.
