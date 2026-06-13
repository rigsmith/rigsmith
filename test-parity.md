# Test Parity Tracker

Tracks the two .NET source projects' test suites against the Go port. Tick a box
when the Go port has **equivalent functional coverage** — not a line-for-line
translation, but a test that asserts the same behavior.

- **Source A:** `~/Git/net-changesets` (C#, NUnit) → Go `changerig` + `relrig` + `core`
- **Source B:** `~/Git/rig` (C#, MSTest) → Go `rig` + `core`
- **Go port:** `~/Git/rigsmith` (stdlib `testing`, table-driven, `t.TempDir()` fixtures)

## Status legend

- `[x]` **DONE** — Go has a named test asserting the same behavior.
- `[~]` **PARTIAL** — some Go coverage exists; specific cases still missing (noted).
- `[ ]` **TODO** — no Go coverage yet.
- `⛔ N/A` — intentionally does not port (see why below).

### What intentionally does **not** port

Per [[rigsmith-no-dotnet-compat]] and the polyglot north star ([[project-north-star]]):

- **`.net.mkd` interop extension** + mixed .NET/Node changeset cleanup — the Go
  tool is polyglot-native; there is no privileged .NET path and no Node-tool
  delegation, so the interop-file tests are N/A.
- **`NodeChangesetService` / "delegate to npx changeset"** — the Go engine *is*
  the engine ([[eliminate-node-engine-design]]); it doesn't shell out to the
  Node CLI, so delegation-guard tests are N/A.
- **`ProcessExecutor` internals** — different process model (Go `os/exec`); the
  behavior is covered implicitly by command tests, not a dedicated unit.
- **csproj-specific tests** collapse into the `dotnet` *ecosystem adapter* (one
  of four), not a first-class path.

## Comparing the two suites — the feasible form

A direct C#↔Go test diff isn't feasible. The feasible cross-check is **shared
golden fixtures**: a language-neutral corpus of input cases (a workspace tree +
changesets) with expected output goldens (release plan JSON, rendered CHANGELOG).
net-changesets already has an `E2E/Parity` suite that runs the real Node
`changeset` and diffs output — that same corpus can become `core/testdata/parity/`
that *both* the C# tests and the Go `testscript`/golden tests read. That turns
"do they match?" into a committed artifact instead of a manual comparison. See
the **Parity corpus** section at the bottom — it's the highest-leverage item.

---

## Scoreboard

| Area | .NET tests | Go status |
|---|---:|---|
| **core: semver** | 17 | ✅ done |
| **core: changeset parse/render** | 7 | ✅ done (interop N/A; polyglot cleanup done) |
| **core: planner (cascade/group)** | 20 | ✅ done |
| **core: release/pre/snapshot planning** | 14 | ✅ done |
| **core: changelog rendering/formatter** | 43 | ✅ done (built `core/mdfmt` + `core/changelog`) |
| **core: dotnet ecosystem (csproj)** | 13 | ✅ done |
| **core: node ecosystem (workspaces)** | 7 | ✅ done |
| **core: config / git / prestate / since** | 19 | ✅ done |
| **changerig: CLI commands** | 47 | ✅ done (cmdtest, 31 tests; TTY/interop N/A) |
| **relrig: release pipeline + publish** | 68 | ✅ done (53 ported; NuGet-protocol units N/A — no native feed client) |
| **rig: config/jsonc/dotenv/discovery** | ~90 | ✅ done |
| **rig: verb logic / matching** | ~70 | ✅ done (CIM/real-assembly/TTY cases N/A) |
| **NEW in Go (no .NET ancestor)** | — | ✅ ranges, gomod, cargo, walkutil, topo-sort |

---

# Source A — net-changesets → `changerig` / `relrig` / `core`

## A1. `core` — engine (shared)

### Semver — `Shared/SemverTests.cs` → `core/semver/semver_test.go`
- [x] `TryParse_PlainVersion` → `TestParseRoundTrip`
- [x] `TryParse_PrereleaseAndBuildMetadata` → `TestParseRoundTrip`
- [x] `TryParse_BuildMetadataOnly` → `TestParseRoundTrip`
- [x] `TryParse_InvalidStrings` (5 cases) → `TestParseInvalid`
- [x] `RaisePatch_OnPrerelease_Graduates` → `TestRaisePatch`
- [x] `RaiseMinor_OnPatchZeroPrerelease_Graduates` → `TestRaiseMinor`
- [x] `RaisePatch_OnStable_Increments` → `TestRaisePatch`
- [x] `PrereleaseNumber_ReadsSecondIdentifier` → `TestPrereleaseNumber`
- [x] `WithPrerelease_KeepsCoreAddsLabel` → `TestWithPrerelease`
- [x] `CompareTo_PrereleaseLowerThanStable` → `TestCompare`
- [x] `CompareTo_FollowsSemVerChain` → `TestCompare`
- [x] `CompareTo_IgnoresBuildMetadata` → `TestCompare` (`1.0.0+build` == `1.0.0`)
- ➕ **Go-only:** `TestParseShortForms`, `TestRaiseMajor`, `ranges_test.go`
  (`TestSatisfies`, `TestSatisfiesString`) — npm-style ranges, new for polyglot.

### Changeset format — `ChangesetsRepositoryTests.cs` → `core/changeset/changeset_test.go`
- [x] `CreateChangeset_WritesLowercaseBump` → `TestRenderRoundTrip` (`Bump.String()`
      is lowercase; the round trip would fail otherwise)
- [x] `GetChangesets_DifferentBumpPerName_KeepsEachBump` → `TestRenderRoundTrip`
      (multi-release round trip) + planner `TestOneChangesetDifferentBumpPerName`
- ⛔ `GetChangesets_WithInteropExtension_ReadsBothExtensions` — N/A (no `.net.mkd`)
- ⛔ `Cleanup_PureDotnetInteropFile_IsDeleted` — N/A
- ⛔ `Cleanup_MixedSharedMd_StripsDotnetKeepsNode` — N/A
- ⛔ `Cleanup_MixedInteropFile_RenamedToMdWithNodeRemainder` — N/A
- ⛔ `Cleanup_PureNodeMd_IsLeftUntouched` — N/A (replaced by the polyglot
      consumed-vs-kept semantics below)
- [x] **NEW:** polyglot changeset cleanup → `planner.PartitionChangesets`
      (`core/planner/partition_test.go` ×4) + cmdtest
      `TestVersionKeepsIgnoredOnlyChangeset` / `TestVersionMixedIgnoredChangesetFails`
      / `TestVersionUnknownPackageChangesetFails`. Node-verified (live
      @changesets v3.0.0-next.5): there is no "partial rewrite" — a changeset
      naming only ignored packages is **kept** (not consumed, exit 0); a
      changeset mixing ignored and not-ignored packages is a **hard error**
      ("Mixed changesets … are not allowed"); a changeset naming a package not
      in the workspace is a **hard error**. Fixed a real gap: `version`
      previously deleted every changeset unconditionally (and silently consumed
      unknown-package changesets via the planner's interop-era skip).
- ➕ **Go-only:** `TestParseCRLF`, `TestTypeDrivenChangeset`, `TestBreakingType`,
  `TestConventionalFromSummary`, `TestParseEmpty`

### Release planning / cascade — `Version/ChangelogGeneratorTests.cs` → `core/planner/planner_test.go`
- [x] cascade to dependents → `TestCascadePatchesDependents`
- [x] transitive cascade → `TestTransitiveCascade`
- [x] `ProjectWithTwoDependents_BumpsBoth` → `TestTwoDependentsBothPatchBump`
- [x] `OneProjectTwoPatches_UpdatesSemVerByOne` → `TestTwoPatchesBumpOnce`
- [x] `UpdateInternalDependenciesMinor_StillPatchBumps` → `TestUpdateInternalDependenciesMinor`
- [x] `OneChangesetDifferentBumpPerName_AppliesEachBump` → `TestOneChangesetDifferentBumpPerName`
- [x] `IgnoredPackageByName_Excluded` → `TestIgnoreFiltersPackage` (uses a `*Bench` glob, so glob is covered too)
- [x] `IgnoredPackageByGlob_Excluded` → `TestIgnoreFiltersPackage`
- [x] `IgnoredDependent_NotBumped` → `TestIgnoredDependentRangeOnly` + parity
      `ignored-dependent` (Node-verified: ignored dependent is a "none" release —
      not bumped, no changelog, but its manifest dep range IS rewritten)
- [x] `FixedGroup_ReleasesEveryMember` → `TestFixedGroupReleasesAllMembers` +
      parity `fixed-group`/`fixed-group-highest` (incl. changelog goldens)
- [x] `LinkedGroup_CoordinatesReleasingMembersToHighest` →
      `TestLinkedGroupPartialRelease` + parity `linked-group-partial`
- [x] `LinkedGroup_AllReleasingShareHighestBumpAndVersion` →
      `TestLinkedGroupSharesHighestBumpAndVersion` + parity `linked-group`
- [x] `FixedGroupInPrerelease_CoordinatesHighestPrereleaseCounter` →
      `TestFixedGroupInPrereleaseCoordinatesCounter`
- [x] `SharedPropsLockstep_CoordinatesToHighestBump` →
      `TestLockstepSharedVersionFileCoordinatesToHighestBump`
- **Changelog-text output** (now exercised end-to-end by the parity corpus):
  - [x] `DependentModule_UsesNestedUpdatedDependenciesFormat` → parity
        `dependency-patch`/`own-change-plus-dependency` goldens
  - [x] `DependentOnMultipleChangedProjects_GroupsUpdatedDependencies` → corpus
        `dependency-multi`
  - [x] `ProjectWithPackageId_UsesItAsDisplayNameKeepsIdentity` → `TestDisplayNameKeepsIdentity`
  - [x] `DependentEntry_ReferencesChangedProjectByPackageId` → `TestDependentReferencesDisplayName`
- ➕ **Go-only:** `TestRangeAwareInRangeNoBump`, `TestRangeAwareOutOfRangeBumps`,
  `TestPeerDependencyForcesMajor`, `TestDevDependencyNoRelease` (npm dep semantics)

### Pre / snapshot planning — `ReleasePlannerTests.cs` + `ReleaseVersionPlannerTests.cs` + `SnapshotSuffixTests.cs` → `core/planner/release_test.go`
- [x] `Assemble_Normal_ResolvesStableBump` → `TestNormalModeResolvesStableBump`
- [x] `Assemble_Pre_ResolvesPrerelease` → `TestPreVersion`
- [x] `Assemble_Snapshot_ResolvesThrowaway` → `TestSnapshotVersion`
- [x] `Assemble_Exit_GraduatesPrereleaseNoChangeset` → `TestGraduatePrereleases`
- [x] `Assemble_Exit_LeavesStableNoChangesetAlone` → `TestGraduatePrereleases`
      (the stable package is asserted absent from the graduation plan)
- [x] `PreVersion_FromStable_CounterZero` → `TestPreVersion`
- [x] `PreVersion_FromExistingPrerelease_Advances` → `TestPreVersion`
- [x] `SnapshotVersion_ByDefault_ZeroBase` → `TestSnapshotVersion`
- [x] `SnapshotVersion_UseCalculatedVersion_BumpedBase` → `TestSnapshotVersion`
- [x] `SnapshotSuffix_NoTemplate_JoinsTagDatetime` → `TestSnapshotSuffix`
- [x] `SnapshotSuffix_NoTemplateOrTag_DatetimeOnly` → `TestSnapshotSuffix`
- [x] `SnapshotSuffix_WithTemplate_FillsPlaceholders` → `TestSnapshotSuffix`
      (all four placeholders incl. `{timestamp}`)
- [x] `SnapshotSuffix_TagGivenButTemplateLacksPlaceholder_Throws` → `TestSnapshotSuffix`
- [x] `SnapshotSuffix_PlaceholderNoValue_Throws` → `TestSnapshotSuffix`

### Changelog rendering & formatting — ✅ DONE (Phase 3, 2026-06-12)
Tracks [[changelog-formatter-native-design]] and [[changelog-generator-plugins-design]].
The Go code did not exist; built + tested this phase: `core/mdfmt` (native
formatter + dispatch), `core/changelog` (setting/resolver/release-line +
extracted file writer), wired into `changerig version` (enrichment before
planning; formatting pass over touched changelogs after writes).

`Version/NativeMarkdownFormatterTests.cs` (18) → `core/mdfmt/mdfmt_test.go`:
- [x] all 18 ported with the exact C# input/expected literals, same test names
      (`TestInsertsBlankLineBetweenVersionAndSectionHeadings` …
      `TestDoesNotTreatIndentedHashAsHeading`); every case additionally asserts
      idempotency (`Format(Format(x)) == Format(x)`). Rune-width ranges
      hand-ported (no dependency); .NET `ReplaceLineEndings` set mirrored
      (CRLF/CR/NEL/FF/LS/PS).

`Version/ChangelogFormatterTests.cs` (10) → `core/mdfmt/dispatch_test.go`:
- [x] all 10 (`TestWhenDisabledDoesNothing` …
      `TestNativeRewritesInPlaceWithoutRunningProcess`) with a fake Runner;
      detection order (dprint→deno→oxfmt→biome→prettier), lockfile→package
      manager (pnpm exec / yarn / bun x / npx), deno direct, graceful
      degradation. `config.FormatSpec()` added (false/absent→"", string→itself,
      true→"true" unknown-warn path) + cases in `TestParseFormatBoolOrString`.

`Version/ChangelogFileWriterTests.cs` (3) → `core/changelog/writer_test.go`:
- [x] `GeneratesNewFile_WhenAbsent` → `TestWriteEntryGeneratesNewFileWhenAbsent`
- [x] `AmendsExistingFile_WhenExists` → `TestWriteEntryAmendsExistingFileNewestOnTop`
- [x] `GeneratesTwoChangelogs_ForMultipleProjects` → `TestWriteEntryGeneratesTwoChangelogsForMultipleProjects`
      (`changelog.WriteEntry` extracted from `changerig/commands/version.go`;
      byte layout unchanged — parity goldens still green)

`Version/ChangelogCommitResolverTests.cs` (5) → `core/changelog/resolver_test.go`:
- [x] all 5 with a fake Runner (`git log --diff-filter=A` + `.net.mkd`
      fallback, gh PR/author lookups, "null"-literal filtering, failures
      degrade to empty fields)

`Version/ChangelogReleaseLineTests.cs` (7) → `core/changelog/line_test.go`:
- [x] all 7 (git short-hash prefix, first-line-only, GitHub PR/commit/Thanks
      link wrapper with independently-omitted parts, unchanged fallbacks)

Wiring notes: the three @changesets generator ids (`@changesets/cli/changelog`,
`-git`, `-github`) all render the **default layout** — git/github only decorate
release lines, applied to changeset summaries before `Plan`. This also fixed a
latent bug: those ids previously fell through to subprocess plugin resolution.
E2E-verified: changelog-git + format:native through the real binary produces
the decorated, prettier-formatted output.

### dotnet ecosystem (csproj) — `CsProjectsRepositoryTests` + `CsProjectStrategyTests` + `ProjectVersionResolverTests` → `core/ecosystem/dotnet/dotnet_test.go`
- [x] `UpdateModuleCsProjs_IncreasesVersion` → `TestSetVersionInline`
- [x] `AddsInlineVersion_WhenNone` → `TestSetVersionInsertsWhenAbsent`
- [x] `WritesVersionPrefix_LeavesSuffix` → `TestSetVersionWritesPrefixLeavesSuffix`
- [x] `WritesSharedPropsOnce_ForLockstepGroup` → cmdtest
      `TestVersionLockstepWritesSharedPropsOnce` (Go writes per module —
      idempotent regex rewrite, not a dedup pass — so the contract is asserted
      as the end state: exactly one `<Version>1.1.0</Version>` in the props,
      no inline version added to either csproj)
- [x] `GetCsProjects_OnlyValidVersion` → `TestDiscover`
- [x] `CsProjectStrategy_ToIndependent_RedirectsSharedToOwnCsproj` →
      `TestVersionStrategyLockstepVsIndependent` (planner clears `VersionFile`,
      so SetVersion targets the manifest) + cmdtest
      `TestVersionIndependentWritesInline` (e2e: inline writes, props untouched).
      The strategy lives in the planner, not a CsProjectStrategy class —
      landed with `version --independent`; this entry had gone stale.
- [x] `CsProjectStrategy_ToIndependent_LeavesInlineUntouched` → inline-package
      case in `TestVersionStrategyLockstepVsIndependent` (already-inline C is
      unchanged under both strategies: own bump, own manifest)
- [x] `Resolve_InlineVersion_IsIndependent` → `TestDiscover`
- [x] `Resolve_VersionPrefix_UsesPrefixElement` → `TestDiscover`
- [x] `Resolve_FromDirectoryBuildProps_IsShared` → `TestDiscover` (inheritance case)
- [x] `Resolve_NoVersionAnywhere_ReturnsNull` → `TestDiscoverSkipsProjectWithNoVersion`
- ➕ **Go-only:** `TestPublishPrivateSkipped`

### node ecosystem (workspaces) — `NodePackagesRepositoryTests.cs` → `core/ecosystem/node/node_test.go`
- [x] `ResolvesNpmWorkspaceGlobs_ExcludingRootAndNonWorkspace` → `TestDiscoverNpmWorkspaceGlobs`
- [x] `SupportsYarnObjectWorkspaces_AndDoubleStarGlobs` → `TestDiscoverYarnObjectWorkspaces`
- [x] `HonorsNegatedWorkspacePatterns` → `TestDiscoverNegatedWorkspacePatterns`
- [x] `ReadsPnpmWorkspaceYaml` → `TestDiscoverPnpmWorkspace`
- [x] `NoWorkspaces_FallsBackToWalkingTree_SkippingNodeModules` → `TestDiscoverFallbackWalk`
- [x] `SkipsWorkspacePackagesWithoutName` → `TestDiscoverSkipsWorkspacePackagesWithoutName`
- [x] `MissingDirectory_ReturnsEmpty` → `TestDiscoverMissingDirectoryReturnsEmpty`
- **Note:** landing these ported the C# glob engine into `node.go` (`**`, `!`
  negation — `filepath.Glob` supports neither) and stopped discovering the
  workspace ROOT package.json as a package (matches npm/yarn/@manypkg and the
  C# repository; parity corpus unaffected).
- ➕ **Go-only:** `TestSetVersionAndDeps`, `TestPublishPrivateSkipped`

### config / git / prestate / since — ✅ DONE (`since` consumers: `status --since` ×3 + `add --since`, see A2)
`Shared/ConfigurationServiceTests.cs` (5) → `core/config/config_test.go`:
- [x] `ReadsSharedKeysAndNestedDotnet` → `TestParseSharedKeysAndEcosystemBlocks`
- [x] `ReadsFormatKey_AsBoolOrString` → `TestParseFormatBoolOrString`
- ⛔ `LegacyFlatKeys_MigratedToDotnet` — N/A (the Go config never had the C#
      tool's legacy flat keys; nothing to migrate)
- [x] `UnknownAndMissingKeys_ToleratedWithDefaults` → `TestParseUnknownAndMissingKeysTolerated`
      (+ `TestDefaults`)
- [x] `CreateDefault_WritesDualToolConfigThatRoundTrips` → `TestDefaults` (values)
      + cmdtest `TestInitConfigRoundTrips` (the config `init` writes parses back
      to the documented defaults; the C# `dotnet.sourcePath` half is N/A — the
      Go default config has no privileged dotnet block)

`Shared/ChangelogConfigTests.cs` (5) → `core/config/config_test.go`:
- [x] all five shapes (git string / github array / false / stock string / absent)
      → `TestChangelogSpec`

`Shared/GitServiceTests.cs` (4) → `core/gitutil/gitutil_test.go` (real temp repos):
- [x] `GetChangedFilesSince_DiffsFromMergeBase_FullPaths` →
      `TestChangedFilesSinceDiffsFromMergeBaseFullPaths` (**new API**
      `gitutil.ChangedFilesSince` — merge-base diff incl. uncommitted tracked
      edits, absolute paths, `--no-relative`)
- [x] `GetChangedFilesSince_InvalidRef_ReturnsNull` → `TestChangedFilesSinceInvalidRef`
      (Go returns an error instead of null)
- [x] `GetAllTags_ReturnsTagNames` → no separate AllTags API in Go; tag listing is
      exercised by `TestLatestModuleVersion` + `TestTagExistsAndCreateTag`
- [x] `CreateTag_RunsGitTagWithMessage` → `TestTagExistsAndCreateTag` (annotated,
      message asserted, existing-tag no-op, empty message defaults to tag name)
- ➕ also: `TestModuleTag`, `TestShortHead`, `TestDefaultRemote`

`Shared/PreStateRepositoryTests.cs` (3) → `core/prestate/prestate_test.go`:
- [x] `Read_WhenNoFile_ReturnsNull` → `TestReadNoFileReturnsNil`
- [x] `Write_ThenRead_RoundTrips` → `TestWriteThenReadRoundTrips` (asserts the
      JS-shared on-disk shape: two-space indent, key set, trailing newline)
- [x] `Remove_DeletesFile` → `TestRemoveDeletesFile`

`Shared/SinceChangesTests.cs` (2) → `core/since/since_test.go` (new package,
ports SinceChanges.cs; consumed by `status --since` + `add --since`):
- [x] `ChangedProjectNames_ReturnsProjectsOwningChangedFile` →
      `TestChangedProjectNamesReturnsProjectsOwningChangedFile`
- [x] `AnyChangesetAdded_DetectsChangesetFiles_NotReadme` →
      `TestAnyChangesetAddedDetectsChangesetFilesNotReadme` (+ `TestChangedChangesetIDs`)

## A2. `changerig` — changeset CLI commands — ✅ DONE (Phase 4, 2026-06-12)

Binary-driven tests in `changerig/cmdtest/` (TestMain builds changerig AND
relrig once; real npm-workspace + git fixtures), mirroring the C#
`CommandAppTester` suites. Interop/.net.mkd, Node-delegation, and
interactive-TTY prompt cases are ⛔ N/A throughout (huh/bubbletea prompts need
a TTY; the non-interactive flag forms cover the contracts).

- **Add** — [x] `--empty`, `-m`+`-p` (patch default), `--bump` override,
  `--type` (bump derives), not-initialized error → `TestAdd*` ×5.
  **NEW:** `add --since <ref>` preselects the changed packages in the picker
  (wired this phase; interactive, so exercised via the `since` package tests).
  ⛔ `--open` (no Go flag), interactive happy paths.
- **Init** — [x] creates folder+config+README, re-run reports
  already-initialized (exit 0, message-differentiated — C# used result codes),
  regenerates a deleted config → `TestInit*` ×3.
- **Info** — [x] config+package listing; before-init reports
  `initialized: false` with exit 0 (Go's choice; C# errored) → `TestInfo*` ×2.
- **Pre** — [x] all 6: enter writes pre.json (mode/tag/initialVersions),
  no-tag fails, enter-twice fails (original tag survives), exit flips mode,
  exit-when-not-pre fails, unknown action fails → `TestPre*`.
- **Status** — [x] all (the Go set): present→0 / absent→non-zero
  ("no changesets found"), `--verbose` summaries, `--output` JSON plan,
  **`--since`** ×3 (narrows to since-changesets; changed-project-without-
  changeset guard fails with `add` guidance; invalid ref fails), and pre-mode
  reflection (status shows the same prerelease target `version` would write —
  `assemblePlan` now mirrors version's mode handling) → `TestStatus*` ×8.
- **Tag** (relrig) — [x] tag per package (`name@version`), skip existing +
  idempotent re-run, **honor ignore** → `TestTag*` ×3. The ignore case exposed
  a real gap: `tag` (and `publish`) looped every package without consulting
  `ignore` — fixed via the new `config.IsIgnored` (planner's glob matcher
  promoted to config and shared). ⛔ PackageId case (no npm analogue).
- **Ui** — [x] non-interactive guard fails fast without hanging (Setsid, no
  TTY) → `TestUINonInteractiveFailsFast`. ⛔ runs-selected-command (needs TTY).
- **Version** — [x] `--dry-run` writes nothing; no-changesets no-op → ×2.
  Full normal/snapshot/pre flows live in the parity suite.
- **Dispatcher** — [x] unknown subcommand → non-zero + usage error.
- ⛔ `ProcessExecutorTests` (2), `NodeChangesetServiceTests` (3) — N/A.

## A3. `relrig` — release orchestrator — ✅ pipeline DONE (Phase 5, 2026-06-12)

Built this phase: `core/jsonc` (tolerant JSONC parse, offset-preserving Strip),
`release/internal/pipeline` (config/resolve/run/vars/masker/uimode/reporter),
`release/internal/forge`, the rich reporter + `relrig release` command, and the
publish confirm gate.

- **ReleasePipeline** (28) → [x] `release/internal/pipeline` (pipeline_test.go
  et al., 28/28 with fake runner/reporter/prompter): step resolution + builtin
  delegation (`${tool} version|publish`, git commit/push argv), custom run
  steps, unknown-step errors, `--only/--skip/--from/--to` with the exact
  skip-reason precedence, before/action/after ordering, onError hooks +
  skip-later, `${tool}`/`${vars.*}`/`${env.*}` interpolation (unknown verbatim),
  lazy/eager var capture + caching, secret masking (longest-first `***`),
  dry-run no-op (no vars), confirm gate (decline = cancel, no onError), native
  handler invoke/fail/no-handler-skip.
- **ReleaseConfigService** (6) → [x] config_test.go (`.changeset/release.jsonc`
  via `core/jsonc`; CommandSpec string=shell / array=argv; command lists mix
  both; confirm bool-or-string; missing→empty; invalid→error).
- **ReleaseUiMode** (6) → [x] uimode_test.go (`rich = !noUi && (ui ||
  !outputRedirected)`; `interactive = !yes && !outRedir && !inRedir`).
- **PlainReleaseReporter** (2) → [x] reporter_test.go (plan + resume hint
  `Resume with: <tool> release --from <lastStarted>`, masking).
- **TuiReleaseReporter** (4) → [x] `release/internal/cli/reporter_test.go`
  (lipgloss `richReporter`: plan w/ gates+skips+native line, failure panel w/
  resume hint, success panel w/o hint, masking everywhere) — content asserted,
  styling not.
- **PlanChooser** (1) → [x] passthrough (`PassthroughChooser`); the interactive
  TUI step-toggle chooser is deferred (noted below).
- **ForgeReleaseService** (6) → [x] `release/internal/forge` (fake gh/git:
  none-mode, create-for-missing-tag, skip-existing, non-github origin skip,
  auto+gh-ready creates, CHANGELOG `## <version>` section as notes w/ tag
  fallback; ignored packages filtered).
- **`relrig release` command** → wired: `--dry-run --only --skip --from --to
  --config -y/--yes --git-only --ui --no-ui`; rich vs plain by TTY detection;
  interactive gates via huh on a TTY, `FixedPrompter(--yes)` otherwise;
  githubRelease native handler → forge. `tool` defaults to `relrig` (the Go
  binary IS the engine; set `"tool": "npx changeset"` to drive the Node CLI).
  E2E-verified piped + `--yes` + gate-decline + masking + resume hint.
- **Publish confirm gate** → [x] `relrig publish` now prompts before the first
  real side effect when stdin+stdout are TTYs; `--yes`/non-TTY/`--dry-run`
  skip it (CI behavior unchanged — per decision).
- **Publish** (`PublishChangesetCommandTests` 9 + `DotnetServiceTests` 2) →
  [x] the Go publish delegates to ecosystem adapters (each has
  `TestPublishPrivateSkipped`); cmdtest covers tag flows + ignore.
  ⛔ `NuGetPackageRegistryTests` 4 — N/A under the current design: the C# tool
  speaks the NuGet V3 flat-container protocol natively (lowercase id,
  404→empty, service-index base-address), while the Go dotnet adapter has no
  HTTP feed client at all — idempotency is `dotnet nuget push
  --skip-duplicate`, so the protocol behaviors live inside the dotnet CLI.
  These four only become portable if a native feed client ever lands.
  ⛔ interop auto-run-node N/A.
- Deferred (lower value): the interactive plan-chooser TUI (toggle steps before
  the run when rich+interactive).

---

# Source B — rig (.NET) → `rig` (Go) — ✅ DONE (Phase 6, 2026-06-12)

160 Go tests in `cli/internal/...`. Built this phase: the comment-preserving
JSONC editor (`core/jsonc.Set`), the full RigConfig schema (JSONC, merge,
namespaces, rich command forms), `cli/internal/envstack` (dotenv + layering,
wired into every spawn path), the C# root-resolution precedence, .NET project
discovery (`detect/dotnet.go`: slnx/sln/conventions), capabilities probing,
the prefix/watch pre-parse pipeline, the dotnet verb-logic layer
(`dotnetverbs.go`), a `rig publish` verb, the .NET coverage `--min` gate +
in-process cobertura HTML, the ConfigWriter, and the default-project setter.

### Matching / navigation
- [x] `CdTests.cs` (5) → `TestRank*` ×13 (superset).
- [x] `GlobTests.cs` (9 cases) → `glob_test.go` (canonical anchored
      case-insensitive `*`/`?` in `detect.GlobMatch`; cli delegates).
- [x] `PrefixResolverTests.cs` (7) → `prefix_test.go` (+ `resolvePrefix`/
      `expandWatch` and the Execute pre-parse pipeline implemented).

### Discovery / root resolution
- [x] `ProjectDiscoveryTests.cs` (7) → `detect/dotnet_test.go`
      (slnx classify, exclude globs, IsExcluded, classic-sln parse, *Tests
      convention, csproj fallback skip bin/, configured-solution override);
      `TestNearestEcosystem_*` retained. sln/slnx now count as a .NET signal
      in `ecosystemsInDir`.
- [x] `RootResolverTests.cs` (6) → `detect/root_test.go` — `Root` rewritten to
      the C# precedence (`.rig.json` > workspace manifest/solution > git root,
      `.git`-file worktrees count, git boundary stops the climb).
- [x] `CapabilitiesTests.cs` (4) → `detect/capabilities_test.go`
      (+ menu gating wired in ui.go).

### Config / JSONC / env
- [x] `RigConfigTests.cs` (15) → `config_test.go` — full schema (Solution,
      Test, Coverage, Rebuild, Publish, EnvPresets, Aliases, rich Commands),
      JSONC parse, unknown-key did-you-mean on `Config.Warnings`,
      `Merge` (repo wins, dict union, blank-string fall-through, pointer
      coalesce), dotnet-namespace fold (beats legacy), node ignored,
      command string/argv/object + per-OS (`macos`/`windows`/`linux`).
- [x] `JsoncEditorTests.cs` (13) → `core/jsonc/editor_test.go`
      (`jsonc.Set`/`SetTopLevelString`: comment-preserving splice, byte
      offsets, create-parent, BOM, refuse-non-object-parent).
- [x] `ConfigWriterTests.cs` (5) → `config/writer_test.go`
      (`SetRepoString`/`Set*`: fresh file w/ `$schema`, splice, refuse-clobber).
- [x] `ConventionTests.cs` (11) → `writer_test.go` + `conventions_test.go` +
      `detect/solution_test.go` (default-setter persists/rejects, rebuild
      bin/obj scoping + dry-run + within-root guard — now wired into
      `runRebuild`, slnx-before-sln, runsettings single/ambiguous/none).
      The interactive `rig default` verb itself is unported (setter only).
- [x] `DotEnvTests.cs` (8) + `EnvStackTests.cs` (2) → `envstack/` — exact C#
      quoting/comment/export rules; precedence (low→high) file < ambient <
      config < command (note: ambient beats file, per the C#); wired into
      `commandEnv` + `customEnv` so every spawned command gets the layers.

### Coverage / doctor
- [x] `CoverageTests.cs` (8) → `coverage_test.go` — parsing (pre-existing) +
      `meetsMinimum` gate (the .NET `--min` gate is now implemented),
      `resolveCoverageOptions` CLI-over-config, runner explicit/auto-MTP via
      global.json, collect-args by runner + filter-when-scoped, cobertura
      `readCoberturaRates`, in-process HTML render (stand-in for
      ReportGenerator.Core).
- [x] `DoctorTests.cs` (5) → `doctor_test.go` — `SdkSatisfies` (incl.
      defers-when-pin-absent), `readSdkPin` (now walks ancestors).

### Verb logic — `VerbLogicTests.cs` (34) → ✅ `dotnetverbs_test.go` + `kill_test.go`
- [x] **Kill** — config-match-wins (fixed: arg no longer beats `kill.match`),
      bare-kill sweeps runnable projects only (fixed for .NET), kill-named,
      project-name-not-assembly, lsof/netstat/self-filter. ⛔ the Windows
      CIM/command-line cases (Go uses `taskkill /IM` only — feature gap noted).
- [x] **Run/Test** (10) — resolution (sole/default/ambiguous/query/none),
      run-arg ordering (framework/launch-profile before `--`), watch prepend,
      test framework+filter shorthand, vstest-positional vs MTP `--project`.
- [x] **Rebuild** (2), **Publish** (2, + the `rig publish` verb itself),
      **Add/Remove/Global/Dlx** (5), **Update/Outdated** (5), **Win exec** (1),
      **configuration passing** (1).

### Other rig tests
- [x] `InfoInitTests.cs` (4) → `infoinit_test.go` (init template,
      refuse-overwrite [Go exits 0 w/ message; C# exited 1], info-on-empty,
      coverage-defaults summary).
- [x] `MenuInputTests.cs` (4) → `ui_test.go` via `menuModel.Update`
      (escape/backspace cancel, submenu pop, passthrough). ⛔ async-read case
      (no async input path in bubbletea).
- [x] `IntegrationTests.cs` (5 of 6) → `scripts_test.go` in-process
      (shell/argv exit codes, arg forwarding, env, missing-OS-spec error).
      ⛔ real-`dotnet`-build E2E (SDK spawn; covered generically).
- [x] `TestEnumerationTests.cs` → `detect/dotnet_test.go` at the
      csproj-classification level (mstest/nunit/xunit/IsTestProject/*Tests).
      ⛔ real-assembly enumeration + cross-TFM gate (needs a .NET metadata
      reader; no prebuilt fixtures to port against).
- ⛔ `SmokeTests.cs` (1) — N/A (harness-wiring smoke).

➕ **Go-only `rig` tests (no .NET ancestor):** `workspace_test.go`
(`TestTopoSortDepsFirst`, `TestTopoSortCycleTolerant`, `TestFilterTargets`,
`TestMatchTarget`) — monorepo topo-sort is new in the Go tool.

---

# Parity corpus — the cross-comparison artifact ✅ LANDED

net-changesets' `E2E/Parity` suite is now a shared, language-neutral corpus that
the Go port verifies against the same Node-frozen goldens.

```
core/testdata/parity/
  scenarios.json                       # packages + changesets + expectedVersions (Oracle 1)
  golden/<scenario>/<pkg>/CHANGELOG.md # frozen Node @changesets output (Oracle 2)
  README.md                            # provenance + regeneration
changerig/parity/harness_test.go       # builds changerig, runs both oracles
```

- [x] Extract the 10 "matching" scenarios (`ParityScenarios.Matching`) + their
      Node goldens into `core/testdata/parity/`.
- [x] Go harness `TestParity`: builds the `changerig` binary, materializes each
      scenario as a node workspace, runs `version`, asserts package versions
      (Oracle 1) and per-package `CHANGELOG.md` byte-for-byte vs golden (Oracle 2).
      Unreleased packages (no golden) must produce no CHANGELOG.
- [x] `status --output` release-plan JSON oracle — added `--output` to Go
      `changerig status` (emits `{releases:[{name,type,newVersion}]}`, matching
      @changesets); harness `TestStatusPlan` asserts it lists exactly the changing
      packages at the right versions.
- [x] `dependency-multi` scenario — dependent of two changed packages groups both
      under one "Updated dependencies" header (covers
      `DependentOnMultipleChangedProjects_GroupsUpdatedDependencies`).
- [x] `scripts/regen-parity-goldens.mjs` — repo-local golden regenerator/verifier
      against live Node (`--write` freezes new scenarios; bare run diffs all 20
      and exits non-zero on drift).
- [x] `in-range-no-release` scenario — `^1.0.0` dependent not released on an
      in-range patch (Go matches Node + net-changesets).
- [x] Oracle 3 — manifest dependency-range rewrite (`expectedRanges`): asserts the
      rewritten in-repo dep ranges in each `package.json` match Node (e.g.
      `1.0.0`→`1.1.0`; in-range/unreleased left untouched).
- [x] Prerelease lifecycle parity (`TestPrereleaseParity`, covers
      `PreReleaseAndSnapshotParityTests` pre half): enter → version → +changeset →
      version → exit → version, version + changelog checked at each step vs Node
      goldens (`prerelease/step{1,2,3}`). Validates `prestate`/`ApplyPre`/
      `GraduatePrereleases` end-to-end.
- **21 scenarios + prerelease flow + snapshot; TestParity (3 oracles) +
  TestStatusPlan + TestPrereleaseParity + TestSnapshotParity +
  TestKnownDivergence, all green.**
- [x] Snapshot parity (`--snapshot`) — `TestSnapshotParity` asserts shape
      (`0.0.0-canary-<datetime>` regex) + templated changelogs. Caught two real
      divergences (changesets now consumed; dep ranges pinned to the exact
      snapshot version).
- [x] In-range range-rewrite threshold scenarios
      (`in-range-rewrite-at-threshold`, `in-range-below-threshold`,
      `out-of-range-below-threshold`) + planner fix: the threshold gates both
      the range rewrite AND the "Updated dependencies" changelog line for
      in-range deps; out-of-range always rewrites.
- [x] fixed/linked group parity (`fixed-group`, `fixed-group-highest`,
      `linked-group`, `linked-group-partial`) with Node-frozen goldens.
- [x] `ignore` parity (`ignored-dependent`) — Node-verified "none" release
      semantics (range rewritten, no bump/changelog; listed as `type: "none"`
      in `status --output`).
- [x] `transitive-divergence` / `KnownDivergenceTests` ported as a documented,
      asserted exception: the scenario carries a `knownDivergence` marker
      (versions/ranges still asserted; the pkg-c golden is frozen Node output)
      and `TestKnownDivergence` self-polices that Go (which keeps the "Updated
      dependencies" header, like net-changesets) still differs from Node's bare
      nested bullet — converging on Node makes it fail on purpose.
- [x] dotnet ecosystem corpus — `TestDotnetParity` (same scenarios as a csproj
      tree vs the SAME Node goldens; npm-range scenarios excluded) +
      `TestDotnetCrossOracle` (runs the real net-changesets C# CLI and changerig
      on identical fixtures, byte-identical required; skips when the C# tool
      isn't built). 18 applicable scenarios, all green both ways.
- [x] `fixed-group-dependent-cascade` — a dependent of a fixed-PULLED member
      cascades (Node-verified; bug 7 below). Carries `knownDivergence` (Node's
      header quirk) AND `netDivergence` (net-changesets doesn't re-cascade
      after group coordination; the cross-oracle skips pkg-c).
- [x] Cross-ecosystem polyglot cascade — `TestPolyglotParity`: one changeset on
      a C# lib releases a fixed group spanning dotnet+node+go+cargo (native
      write-back each) and cascades into an npm dependent of a member.
      Self-authored goldens in `polyglot/` (no external oracle runs mixed
      repos), justified piecewise by the Node/C# oracles.
- [ ] (optional) Point the C# parity tests at this same corpus so one golden set
      is authoritative for both implementations. Scoped (2026-06-12) — the work
      is all on the net-changesets side: (1) deserialize `scenarios.json`
      (System.Text.Json; today's `ParityScenarios` are inline records) incl.
      `expectedRanges`/`knownDivergence`/`netDivergence` and the
      string-or-object dependency form; (2) write `fixed`/`linked`/`ignore`
      into the materialized config.json (C# fixtures don't yet); (3) point
      goldens at `core/testdata/parity/golden/` (22 scenarios frozen there vs
      C#'s 10) and gate the npm-range scenarios (csproj refs are rangeless —
      same `dotnetApplicable` filter as Go's `TestDotnetParity`); (4) skip
      Oracle 3 (manifest ranges are npm-only). Cross-checking already exists
      from the Go side via `TestDotnetCrossOracle`.

### Bugs the corpus has already caught

1. **Changelog blank-line drift** (cosmetic) — Go inserted a blank line between
   `## <version>` and the first `### Section`; Node `format:false` does not. Fixed
   in `core/planner/changelog.go` (`renderSections`). No test had pinned the old format.
2. **`updateInternalDependencies` misapplied as bump level** (real version bug) —
   out-of-range dependents were bumped by the `updateInternalDependencies` value
   instead of always patch. Verified against live Node `@changesets` v3.0.0-next.5
   (App 2.0.0 → **2.0.1**, not 2.1.0, with range rewritten ^1.0.0 → ^2.0.0). Fixed
   in `core/planner/planner.go`; corrected `TestUpdateInternalDependenciesMinor`,
   which had encoded the wrong expectation (2.1.0).
3. **In-range rewrite threshold unmodeled** — Go always rewrote an in-range dep's
   range (and always added the changelog line). Node gates BOTH on
   `depBump ≥ updateInternalDependencies`. Fixed in planner section 3
   (`depLink` gating); covered by the three threshold scenarios.
4. **Snapshot kept changesets** — Node *consumes* changesets on `version
   --snapshot` (the run is throwaway because you never commit). Go kept them.
   Fixed in `changerig/commands/version.go`.
5. **Pre/snapshot dep retargeting** — dep range rewrites and "Updated
   dependencies" changelog lines were computed from the *stable* bump before the
   pre/snapshot override was applied. Node uses the final version (pre keeps the
   operator: `^2.0.0-next.0`; snapshot pins exact: `0.0.0-canary-…`). Fixed via
   `Module.materializeDeps` re-run by `ApplyPre`/`ApplySnapshot`.
6. **"None" releases missing** — an ignored cascade-dependent and a dev-dependent
   of an out-of-range release must still get their manifest ranges rewritten
   (version unchanged, no changelog) and appear in `status --output` as
   `type: "none"`. Go dropped them from the plan entirely. Fixed via
   `Module.RangeOnly`.
7. **No cascade off group-pulled members** — a fixed-group pull moves a member
   out of a dependent's exact range, and Node then patch-bumps that dependent
   (range rewritten, dep changelog line) exactly as if the member had its own
   changeset. Go ran group coordination AFTER the cascade (and after dep-line
   materialization), so the dependent stayed untouched — as does net-changesets
   (a deliberate net divergence now marked `netDivergence` in the corpus).
   Fixed by restructuring `Plan`: cascade + `coordinateGroups`
   (fixed/linked/lockstep) now iterate together to a fixpoint, mirroring
   @changesets `assembleReleasePlan`, and dep materialization runs on the final
   coordinated versions.

---

## How to use this file
1. Pick a section, implement/locate the Go test, flip `[ ]`/`[~]` → `[x]`.
2. Resolve every `[~]` "confirm" note by reading the Go test — either upgrade to
   `[x]` or split out the missing case as a fresh `[ ]`.
3. Priority order: **changelog rendering/formatter (A2)** → **config/git/prestate
   (A1)** → **parity corpus** → command/pipeline tests → rig config/jsonc/env.
