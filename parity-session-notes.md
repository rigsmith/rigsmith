# Parity work — session notes / handoff

Working log for the cross-implementation parity effort. Pick up from **"Where to
resume"** at the bottom. Companion docs: [test-parity.md](test-parity.md) (the full
.NET→Go test inventory + checklist) and
[core/testdata/parity/README.md](core/testdata/parity/README.md) (corpus provenance).

## Goal

Verify the Go port (`rigsmith`) reproduces, byte-for-byte, the behavior of the two
.NET source projects it ports — `net-changesets` (→ `changerig`/`relrig`) and `rig`
(→ `rig`) — and ultimately of upstream Node `@changesets`. The chosen mechanism is a
**shared, language-neutral golden corpus** that multiple implementations assert
against, instead of hand-comparing test suites.

## What exists now (all in the working tree; nothing committed)

The whole `rigsmith` repo is still uncommitted (`git status` = all untracked) — by
prior decision. Do **not** commit without explicit approval.

### The corpus — `core/testdata/parity/`
- `scenarios.json` — **21** language-neutral scenarios (packages, changesets,
  `expectedVersions`, optional `expectedRanges`, optional `fixed`/`linked`/
  `ignore` config, optional `knownDivergence` marker). 12 original + 8 added
  2026-06-12: 3 threshold scenarios (`in-range-rewrite-at-threshold`,
  `in-range-below-threshold`, `out-of-range-below-threshold`), `fixed-group`,
  `fixed-group-highest`, `linked-group`, `linked-group-partial`,
  `ignored-dependent`; + `transitive-divergence` (same day, second session —
  the one deliberate Node mismatch, see Known divergence below).
- `golden/<scenario>/<pkg>/CHANGELOG.md` — frozen Node `@changesets` output
  (config `format:false`, default changelog generator).
- `prerelease/step{1,2,3}/pkg-a/CHANGELOG.md` — goldens for the prerelease flow.
- `README.md` — provenance + the rule: goldens are the **Node oracle**, never
  overwritten by a port's output.
- **`scripts/regen-parity-goldens.mjs`** (new) — repo-local verifier/regenerator:
  bare run diffs every scenario against live Node (exit 1 on drift) and prints
  the resulting versions/ranges; `--write <id>` freezes a new scenario's goldens.

### The harness — `changerig/parity/harness_test.go`
Builds the real `changerig` binary once (`TestMain`), then:
- **`TestParity`** (per scenario) — runs `changerig version`; asserts 3 oracles:
  (1) resulting versions, (2) per-package `CHANGELOG.md` vs golden, (3) rewritten
  in-repo dependency ranges (`expectedRanges`). Packages with no golden must
  produce no CHANGELOG (unreleased).
- **`TestStatusPlan`** (per scenario) — runs `changerig status --output plan.json`;
  asserts the JSON plan lists exactly the changing packages at the right versions.
  Tolerates `type:"none"` entries (Node lists them too) as long as the version is
  unchanged.
- **`TestPrereleaseParity`** — drives `pre enter next → version → +changeset →
  version → pre exit → version`, checking version + changelog at each step.
- **`TestSnapshotParity`** — `version --snapshot canary`; asserts the
  `0.0.0-canary-<14-digit datetime>` shape, exact-pinned dep ranges, untouched
  in-range dependent, consumed changesets, and templated changelogs.
- **`TestKnownDivergence`** (new) — for scenarios with a `knownDivergence`
  marker, asserts Go's changelog still DIFFERS from the frozen Node golden in
  exactly the documented way (self-policing, like net-changesets'
  `KnownDivergenceTests`); `TestParity` skips the byte-compare for marked
  packages but still asserts versions + ranges.

**Status: all green.** Run it with:
```
cd ~/Git/rigsmith && go test ./changerig/parity/ -v
```
(Note: `go test ./...` from the workspace root reports "setup failed" — a go.work
multi-module quirk, not a real failure. Test per module, or use the path above.)

### The Node oracle (for regenerating goldens / probing)
`@changesets` v3.0.0-next.5 is installed (gitignored) at:
```
~/Git/net-changesets/demo/node-sample/node_modules/@changesets/cli/bin.js
```
Run it as `node <that path> <args>` in a materialized workspace. A workspace =
root `package.json` (`workspaces: ["packages/*"]`) + `package-lock.json` (`{}`) +
per-pkg `packages/<name>/package.json` + `.changeset/config.json` +
`.changeset/*.md`. Use `"changelog": "@changesets/cli/changelog"` and
`"format": false` in config to match the existing goldens.

## Bugs found & fixed (all via the corpus, all Node-verified)

1. **Changelog blank-line drift** (cosmetic) — Go inserted a blank line between
   `## <version>` and the first `### Section`. Fixed in
   `core/planner/changelog.go` (`renderSections`: leading `\n` only for sections
   after the first).
2. **`updateInternalDependencies` misapplied as bump level** (real version bug) —
   out-of-range dependents were bumped by the config value instead of always
   patch. Verified against live Node. Fixed in `core/planner/planner.go`
   (`cand = changeset.BumpPatch`); corrected `TestUpdateInternalDependenciesMinor`
   (had asserted 2.1.0; correct is 2.0.1, range rewritten `^1.0.0`→`^2.0.0`).
3. **In-range rewrite threshold unmodeled** (2026-06-12) — Go always rewrote an
   in-range dep's range and always added the "Updated dependencies" line. Node
   gates BOTH on `depBump ≥ updateInternalDependencies` (probe: threshold=minor +
   in-range patch → range stays `^1.0.0`, NO changelog line; threshold=patch →
   `^1.0.1` + line; out-of-range always rewrites even below threshold). Fixed in
   planner section 3 via `depLink` gating + `Module.materializeDeps`.
4. **Snapshot kept changesets** — Node *consumes* changesets on `version
   --snapshot` (only `.changeset/config.json` remains). Go kept them. Fixed in
   `changerig/commands/version.go`. (The old backlog note "changesets kept" was
   wrong — verified against v3.0.0-next.5.)
5. **Pre/snapshot dep retargeting** — DepUpdates + changelog dep lines were
   computed from the stable bump before overrides. Node uses the final version:
   pre keeps the operator (`^1.0.0`→`^2.0.0-next.0`), snapshot pins exact
   (`^1.0.0`→`0.0.0-canary-…`). Fixed: `ApplyPre`/`ApplySnapshot` re-run
   `materializeDeps` after stamping overrides.
6. **"None" releases missing** — Node gives an ignored cascade-dependent AND a
   dev-dependent of an out-of-range release a `type:"none"` release: version
   unchanged, no CHANGELOG, but the manifest dep range IS rewritten, and it
   appears in `status --output`. Go dropped both from the plan. Fixed via
   `Module.RangeOnly` (planner assembly demotes instead of dropping; `version`
   skips changelog for them). Also fixed `--snapshot <tag>` space-form parsing
   (cobra NoOptDefVal only binds `--snapshot=tag`; the tag landed in args).

## Key semantic findings (Node-verified, consistent across all 3 implementations)

- **Dependent bump rule:** in-range dependent → **never released**; out-of-range /
  rangeless dependent → **always patch** (+ range rewritten). Confirmed in Node,
  net-changesets (`ChangelogGenerator.cs:283` hardcodes `BumpType.Patch` with a
  comment saying "updateInternalDependencies is a range threshold… not the bump
  level"), and now Go.
- **`updateInternalDependencies`** does *not* affect release decisions. It is only
  the threshold for rewriting an *in-range* dependency's range spec on a dependent
  that is *already releasing*. net-changesets can't even hit this (csproj refs are
  rangeless); Go's node support is the first to have ranges.
- **Prerelease:** `1.1.0-next.0` → `1.1.0-next.1` (counter advances, sections
  accumulate); `pre exit` + `version` graduates to `1.1.0` with a consolidated
  section (re-listing all changes) atop the retained prerelease history; changesets
  deleted on graduation.

## Known divergence (documented + asserted, by design)

- **`transitive-divergence`** (ported 2026-06-12, second session): on a
  TRANSITIVE dependency entry (pkg-c → pkg-b → pkg-a, pkg-a changes) Node drops
  the "Updated dependencies" header and emits only the bare nested bullet
  (`  - pkg-b@1.0.1`); **Go keeps the header, siding with net-changesets**
  (verified live: versions/ranges agree across all three). The pkg-c golden is
  the frozen Node output; `TestKnownDivergence` fails on purpose if Go ever
  converges — then remove the scenario's `knownDivergence` marker to promote it.

## Session log — 2026-06-12, second session (full-parity push begins)

A phased full-parity roadmap was approved (changeset engine first; **full feature
parity** in scope, including the missing formatter/pipeline features; publish
gets a confirm gate + `--yes` when the Phase-5 pipeline lands). This session
finished the corpus's known-divergence port and the entire "unit tests for
existing core code" phase:

- **`transitive-divergence` ported** (scenario + `knownDivergence` marker +
  `TestKnownDivergence`, goldens frozen from live Node via the regen script).
- **New unit suites for previously test-less packages** (all ported from the C#
  suites; tracker updated): `core/config/config_test.go` (parse/defaults/
  ecosystem blocks/ChangelogSpec/Groups/format shapes), `core/prestate/
  prestate_test.go` (round trip incl. JS on-disk shape), `core/gitutil/
  gitutil_test.go` (real temp repos: tags, ShortHead, DefaultRemote, +
  **new API `gitutil.ChangedFilesSince`** — merge-base diff for the upcoming
  `--since` commands).
- **Planner gaps closed** (10 new tests): two-dependents, two-patches=one-bump,
  per-name bumps, linked-group ×2, fixed-group-in-prerelease counter,
  shared-VersionFile lockstep, DisplayName/PackageId in changelogs ×2,
  normal-mode assembly; `TestSnapshotSuffix` now covers all 5 C# cases.
- **node ecosystem discovery hardened** (port of the C# repository semantics):
  yarn object workspaces, `**` globs, `!` negation (new segment-walking glob
  engine — `filepath.Glob` supports neither), skip-no-name, missing-dir-empty.
  **Behavior change:** the workspace ROOT package.json is no longer discovered
  as a package when workspaces are defined (matches npm/yarn/@manypkg + C#).
- **dotnet + semver small gaps**: VersionPrefix write keeps VersionSuffix,
  no-version-anywhere skipped, `TestWithPrerelease`.
- All suites green per module; ~45 new tests this session.

## Where to resume — backlog (from highest value)

The approved roadmap (phases): corpus completion → ~~core unit tests~~ (done) →
changelog formatting (Phase 3) → changerig command tests (Phase 4) → relrig
release pipeline (Phase 5) → rig dev-CLI (Phase 6). Within that:

1. **dotnet ecosystem corpus** — materialize a csproj tree, run `changerig
   version`, assert csproj versions + CHANGELOG. Oracle = **net-changesets itself**
   (cross-run the C# binary on the same fixture). Realizes the "one corpus, both
   implementations" vision.
2. **Cross-ecosystem polyglot cascade** — node + dotnet/go in one repo, a changeset
   cascading across ecosystems. No external oracle (self-authored goldens); the
   north-star differentiator.
3. **Phase 3: changelog formatting feature parity** (43 C# tests; the Go code
   doesn't exist yet) — `core/mdfmt` native markdown formatter (prettier-
   equivalent block model), formatter dispatch (`format` config: native/auto/
   oxfmt/deno/biome/prettier via lockfile-detected package manager, graceful
   degradation), extract the changelog file writer from
   `changerig/commands/version.go` into core, changelog-git/-github generators
   (commit resolver + release-line decoration, fakes only). A detailed design
   brief from the C# sources is in the approved plan
   (`~/.claude/plans/functional-cooking-tiger.md`).
4. **Snapshot/prerelease e2e leftovers** — `snapshot.useCalculatedVersion` e2e
   (probe Node first) and a two-package prerelease flow golden (pre-mode dep
   retargeting has unit coverage only).
5. **Phase 4: changerig command tests** — init/add/status/version/pre/tag/info
   error paths + `--since` (substrate `gitutil.ChangedFilesSince` is ready;
   wire `status --since`/`add --since` + the SinceChanges logic with it).
6. **Phase 5: relrig release pipeline** — steps/hooks/vars/confirm/forge +
   reporters (design brief in the plan file; publish confirm + `--yes` lands
   here). Build the shared `core/jsonc` parser with it.
7. **Phase 6: rig dev-CLI parity** — JSONC editor, rig config, dotenv/env stack,
   prefix/root resolvers, verb logic (~160 tests).

### Lower-value / poor fit
- `changelog-git` / `changelog-github` generators (env-dependent, non-hermetic) —
  keep as unit tests with fakes.
- `add` command (interactive/TTY-first) — only `--empty`/`-m` are non-interactive.
- `rig` dev-commands (spawn real `dotnet`/`node`) — arg-building logic is better as
  pure-function unit tests.

## Net-changesets reference suite (the parity source of truth)

`~/Git/net-changesets/tests/Changesets.Tests/E2E/Parity/` — `ParityScenarios.cs`,
`ParityFixtures.cs`, `ChangelogGoldenTests.cs` (golden capture via
`RegenerateGoldensFromNode`), `VersionParityTests.cs` (live Node diff),
`StatusParityTests.cs`, `PreReleaseAndSnapshotParityTests.cs`,
`KnownDivergenceTests.cs`. The Go corpus was seeded from its `Golden/` tree.
