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

## What exists now

(The repo is now committed — initial import 2026-06-12, then one commit per
parity phase; see `git log`.)

### The corpus — `core/testdata/parity/`
- `scenarios.json` — **22** language-neutral scenarios (packages, changesets,
  `expectedVersions`, optional `expectedRanges`, optional `fixed`/`linked`/
  `ignore` config, optional `knownDivergence` marker). 12 original + 8 added
  2026-06-12: 3 threshold scenarios (`in-range-rewrite-at-threshold`,
  `in-range-below-threshold`, `out-of-range-below-threshold`), `fixed-group`,
  `fixed-group-highest`, `linked-group`, `linked-group-partial`,
  `ignored-dependent`; + `transitive-divergence` and
  `fixed-group-dependent-cascade` (same day, second session — the deliberate
  Node mismatches, see Known divergence below).
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
7. **No cascade off group-pulled members** (found by the polyglot probe,
   Node-verified) — a fixed pull moves a member out of a dependent's exact
   range; Node patch-bumps that dependent (range rewritten + dep line), Go
   left it untouched because group coordination ran after the cascade. Fixed:
   `Plan` now iterates cascade + `coordinateGroups` (fixed/linked/lockstep
   unified) to a fixpoint, mirroring @changesets `assembleReleasePlan`; dep
   materialization sees the final coordinated versions. NOTE: net-changesets
   shares the old behavior — the first **net divergence** (Go follows Node),
   marked `netDivergence` in scenarios.json; the dotnet cross-oracle skips
   marked packages. Node's changelog for such a dependent drops the "Updated
   dependencies" header (same quirk as transitive) → the scenario also carries
   `knownDivergence`.

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

The approved roadmap (phases): ~~corpus completion~~ (done) → ~~core unit
tests~~ (done) → changelog formatting (Phase 3) → changerig command tests
(Phase 4) → relrig release pipeline (Phase 5) → rig dev-CLI (Phase 6).

*(2026-06-12, second session, later: corpus items 1–2 landed — the dotnet
corpus (`TestDotnetParity` vs the same Node goldens + `TestDotnetCrossOracle`
vs the live C# binary, byte-identical on 18 scenarios; build the oracle with
`dotnet build -c Release` in ~/Git/net-changesets) and the polyglot cascade
(`TestPolyglotParity`, fixed group spanning dotnet+node+go+cargo, self-authored
goldens in `polyglot/`). The probe surfaced planner bug 7 (above). Repo now has
its initial commit; corpus work committed separately.)*

*(2026-06-12, third session: **Phase 3 DONE** — `core/mdfmt` (NativeMarkdownFormatter
port, 18 golden tests, all idempotency-checked, hand-ported rune widths) +
`core/mdfmt/dispatch.go` (formatter dispatch, 10 tests, injectable Runner) +
`core/changelog` (setting/resolver/release-line, 24 tests with fake exec;
`WriteEntry` extracted from version.go, 3 tests). Wired into `changerig
version`: git/github enrichment decorates summaries before `Plan`; the
`format` config drives an mdfmt.FormatFiles pass over touched changelogs
(warn-only). Fixed latent bug: the three @changesets generator ids now map to
the builtin layout instead of subprocess plugin resolution. `config.FormatSpec()`
added. E2E-verified with changelog-git + format:native; parity corpus
unchanged/green.)*

*(2026-06-12, third session cont.: **Phase 4 DONE** — `core/since` (SinceChanges
port), `status --since` (no-changeset guard + narrowing) + status now reflects
pre-mode like version (`assemblePlan`), `add --since` picker preselect, and
`changerig/cmdtest/` (31 binary-driven command tests over both binaries; real
git fixtures). Found + fixed: `tag`/`publish` did not honor `ignore` — added
`config.IsIgnored` (planner's glob matcher promoted) and filtered both loops.
Also: status with no changesets now exits non-zero (the CI gate), and the
`format` config gained a custom-command escape hatch — an argv array
(`"format": ["myfmt", "--write"]`) runs as written via `FormatCommand()` +
`mdfmt.FormatFilesCustom`. Behavior notes vs C#: init/info report state by
message with exit 0 rather than distinct result codes.)*

*(2026-06-12, third session cont.: **Phase 5 DONE** — `core/jsonc` (tolerant
parse, offset-preserving Strip; the comment-preserving editor is Phase 6) +
`release/internal/pipeline` (full C# port: config/resolve/run/vars/masker/
uimode/plain-reporter, 43 tests, injectable runner/prompter/reporter) +
`release/internal/forge` (6 tests) + lipgloss rich reporter (4 tests) + the
`relrig release` command (`--dry-run --only --skip --from --to --config -y
--git-only --ui --no-ui`; TTY-detected rich/plain; huh confirm gates;
githubRelease native → forge; `tool` defaults to relrig itself) + the publish
confirm gate (TTY prompt before first real push; `--yes`/non-TTY/dry-run skip
— per decision). E2E-verified: gates stop piped runs without `--yes`, secrets
masked as `***` everywhere, resume hint on failure. Deferred: interactive
plan-chooser TUI; NuGet feed-protocol adapter units.)*

*(2026-06-12, third session cont.: **Phase 6 DONE — the roadmap's last phase.**
`core/jsonc` editor (comment-preserving Set, 13 tests) · full RigConfig schema
(JSONC, merge w/ ~/.rig.json support, dotnet-namespace fold, did-you-mean
warnings, rich command forms incl. per-OS — consumers updated, 15 tests) ·
`cli/internal/envstack` (exact C# dotenv quoting + file<ambient<config<command
layering, wired into commandEnv/customEnv, 10 tests) · `detect.Root` rewritten
to the C# precedence (.rig.json > solution/manifest > git root) · `detect/
dotnet.go` (slnx/sln parsing, test classification, exclusions, capabilities) ·
prefix/watch pre-parse pipeline · `dotnetverbs.go` (full C# verb-logic port:
run/test/publish/add/remove/global/dlx/update/outdated/win-exec arg builders)
+ a real `rig publish` verb · kill semantics aligned to C# (config-match wins;
runnable-only sweep) · coverage `--min` gate + auto-MTP + in-process cobertura
HTML · doctor ancestor global.json · ConfigWriter + default-setter +
runsettings + rebuild bin/obj scoping wired into runRebuild. ~100 new tests;
cli module now at 160. sln/slnx now count as a .NET ecosystem signal.
Remaining N/A or gaps: Windows CIM kill path, real-assembly test enumeration,
interactive `rig default` verb, MenuInput async case.)*

*(2026-06-12, third session cont.: **rig ergonomics tail DONE** (~58 tests) —
global `~/.rig.json` wired everywhere (`$RIG_GLOBAL_CONFIG` seam, self-merge
guard) · `rig default` (print/picker/persist) · `rig setup` (idempotent shell
integration for zsh/bash/fish: the `rig cd` wrapper + completion sourcing —
NOTE: deliberate divergence, the C# setup is a config walkthrough) · dynamic
project completion on run/test/build/kill/publish/default · per-verb
`--watch`/`-w` on run/build/test · `rig test <Class>` fuzzy matching with the
C# filter shapes (class names via source scan, no CLR) · menu project picker +
focus scoping (verbs run scoped) · Windows CIM kill (command-line matching via
PowerShell, C# parser tests ported) · `rig self-update` (releases/latest vs
the ldflags-stamped version — goreleaser now stamps it — install.sh handoff,
graceful on dev builds). Remaining rig leftovers: C#-style config walkthrough,
real-assembly test enumeration, relrig version seam.)*

*(2026-06-12, third session cont.: **Windows first-class** — CI added
(`.github/workflows/ci.yml`: test matrix ubuntu/macos/windows + vet/gofmt job;
activates when the repo gets a GitHub remote). Custom shell commands now run
OS-native (`cmd.exe /d /s /c` with caret-escaped args on Windows — the old
`sh -c` hardcode + test skips removed); `rig setup powershell` installs the
$PROFILE integration (Set-Location cd wrapper + cobra pwsh completion;
profile path asked from pwsh, `RIG_PWSH_PROFILE` seam). Portability audit
fixed: parity harness binary collision (fixed temp name → per-process dir),
CRLF-robust golden normalize + a root `.gitattributes` (eol=lf — Windows
checkouts stay byte-identical to the goldens), USERPROFILE pinning in setup
tests, a separator-sensitive cmdtest assertion. Residual noted: ecosystem
`relTo` emits OS-native separators across the plugin protocol — needs a
deliberate ToSlash decision; TUI paths unverified under ConPTY until CI runs.)*

1. **Snapshot/prerelease e2e leftovers** — `snapshot.useCalculatedVersion` e2e
   (probe Node first) and a two-package prerelease flow golden (pre-mode dep
   retargeting has unit coverage only).
2. **Small deferred items** — interactive plan-chooser TUI for `release`;
   `packages.versionRegex`; NuGet feed-protocol unit tests if a native feed
   client replaces the nuget CLI delegation; Windows CIM kill; the interactive
   `rig default` verb; point the C# parity tests at the shared corpus
   (optional). Dev-launcher ergonomics tail per FEATURE-PARITY.md (`[suggest]`
   completion, menu pickers, setup/self-update, test-class fuzzy).

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
