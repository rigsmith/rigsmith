# Parity corpus

A language-neutral set of changeset scenarios with **frozen golden output** that
every implementation of the changeset engine must reproduce identically:

- **`@changesets` (Node)** — the upstream reference; the goldens are captured from it.
- **`net-changesets` (C#)** — the prototype, asserts against the same scenarios.
- **`rigsmith` (Go)** — this repo, asserted by `changerig/parity`.

This turns "do the implementations agree?" into a committed artifact instead of a
manual comparison. Two oracles, both independent of any one implementation:

1. **`golden/<scenario>/<package>/CHANGELOG.md`** — the exact changelog text,
   captured from Node `@changesets` with `config.format = false` (raw, un-prettified).
2. **`expectedVersions`** in `scenarios.json` — the version decision, independent
   of changelog formatting.

## Files

- `scenarios.json` — the scenario definitions (packages, changesets, expected versions).
- `golden/` — per-package golden `CHANGELOG.md`, one subtree per scenario.

## Provenance & regeneration

The goldens originate from `net-changesets`' `ChangelogGoldenTests.RegenerateGoldensFromNode`,
which runs the real Node `@changesets version` and freezes its output. **They are
the Node oracle — do not overwrite them with any port's output.** This repo now
has its own regenerator/verifier:

```
node scripts/regen-parity-goldens.mjs              # verify all goldens vs live Node
node scripts/regen-parity-goldens.mjs --write <id> # freeze a NEW scenario's goldens
```

It needs the oracle installed (default: the net-changesets demo's
`node_modules/@changesets/cli/bin.js`, v3.0.0-next.5; override with
`$CHANGESETS_BIN`). Run it without `--write` after touching the corpus — it
exits non-zero on any drift from live Node, and prints each scenario's resulting
versions/ranges so a new scenario's expectations can be filled from observation.

## Comparison normalization

Changelogs are compared after trimming trailing whitespace per line and trailing
blank lines (matching `ParityFixtures.Normalize` in net-changesets). Everything
else — blank lines between sections, bullet indentation — is significant.

## Oracles in the harness

`changerig/parity/harness_test.go` runs three tests:

- **`TestParity`** (per scenario) — runs `changerig version` and asserts three
  oracles: each package's resulting version (1), its `CHANGELOG.md` vs the golden
  (2), and the rewritten in-repo dependency ranges in each manifest (3,
  `expectedRanges`). A package with no golden is one Node did not release; the
  harness asserts no CHANGELOG was written for it.
- **`TestStatusPlan`** (per scenario) — runs `changerig status --output plan.json`
  on a fresh materialization and asserts the JSON plan (`{ releases: [{ name,
  type, newVersion }] }`) lists exactly the packages that change, at the right
  versions.
- **`TestPrereleaseParity`** — drives the full prerelease lifecycle (`pre enter
  next` → `version` → +changeset → `version` → `pre exit` → `version`) and checks
  the version and changelog at each step against goldens in `prerelease/`.
- **`TestSnapshotParity`** — `version --snapshot canary`. The version embeds a
  timestamp, so it asserts shape (`0.0.0-canary-<14-digit datetime>`) and a
  templated changelog instead of frozen goldens. Node-verified: dep ranges are
  pinned to the **exact** snapshot version (operator dropped), changesets are
  **consumed** (not kept), and release decisions still follow stable-version
  math (an in-range dependent stays untouched).
- **`TestDotnetParity`** (`dotnet_test.go`) — the same scenarios materialized as
  a **csproj tree** (mirroring net-changesets' `WriteNetRepo`) must reproduce
  the same Node goldens: the engine's decisions and changelog output are
  ecosystem-independent. Scenarios with explicit npm ranges are excluded
  (ProjectReferences are rangeless), as is the range-rewrite oracle.
- **`TestDotnetCrossOracle`** — runs the **real net-changesets C# CLI** and
  changerig on identical csproj fixtures and requires byte-identical versions
  and changelogs (skipped when the C# tool isn't built; set
  `$NET_CHANGESETS_DLL` or build `~/Git/net-changesets`). Packages under a
  scenario's `netDivergence` marker are skipped (see below).
- **`TestPolyglotParity`** (`polyglot_test.go`) — the north-star scenario: one
  changeset on a C# library releases a fixed group spanning **dotnet + node +
  go + cargo** (each manifest written natively) and cascades onward into an npm
  dependent of a group member. No external oracle can run a mixed repo, so the
  goldens in `polyglot/` are **self-authored**, justified piecewise: version
  math + cascade semantics are Node-verified, the dotnet write-back is
  C#-cross-checked, the changelog format is pinned by the Node goldens.

## Scope (current)

22 scenarios (`fixed`/`linked`/`ignore` config keys are supported in
`scenarios.json` and written into the materialized config):

- 10 "matching" scenarios (single bumps, combined, multiline, dependency cascade,
  0.x).
- `dependency-multi` — a dependent of two changed packages groups both under one
  "Updated dependencies" header.
- `in-range-no-release` — a `^1.0.0` dependent is **not** released when its
  dependency patch-bumps within range (matches @changesets and net-changesets,
  which patch-only because csproj references are rangeless).
- `in-range-rewrite-at-threshold` / `in-range-below-threshold` /
  `out-of-range-below-threshold` — the `updateInternalDependencies` gate: an
  in-range dep of an already-releasing dependent gets its range rewritten (and
  the "Updated dependencies" changelog line) only when its bump ≥ the threshold;
  out-of-range always rewrites + lines, even below the threshold.
- `fixed-group` / `fixed-group-highest` — every member releases at the
  coordinated version (from the highest member version); a member with no
  changeset still gets a bare `## <version>` changelog entry.
- `linked-group` / `linked-group-partial` — releasing members share the highest
  bump from the highest current version; members with no changeset are NOT
  released.
- `ignored-dependent` — an ignored cascade-dependent becomes a "none" release:
  no version change, no CHANGELOG, but its manifest dep range IS rewritten
  (and it appears in `status --output` with `type: "none"`, like Node).

- `fixed-group-dependent-cascade` — a dependent of a **fixed-pulled** member
  cascades (Node-verified): the group pull moves pkg-b out of pkg-c's exact
  range, so pkg-c patch-bumps with the range rewritten, exactly as if pkg-b had
  its own changeset. Carries BOTH markers: `knownDivergence` on pkg-c's
  changelog (Node's header quirk, below) and `netDivergence` on pkg-c
  (net-changesets does not re-cascade after group coordination at all — Go
  follows Node; the dotnet cross-oracle skips the package).
- `transitive-divergence` (pkg-c → pkg-b → pkg-a) — the **known divergence**:
  versions and ranges agree, but Node drops the "Updated dependencies" header
  whenever the dependency's release carries no changeset of its own (transitive
  entries, group-pulled members), emitting only the bare nested bullet; Go
  keeps the header, matching net-changesets. Such scenarios carry a
  `knownDivergence` marker: `TestParity` skips the byte-compare for the marked
  package and `TestKnownDivergence` asserts the outputs still differ in exactly
  that way — if they converge it fails on purpose, and the marker should be
  removed to promote the scenario into the matching set.
