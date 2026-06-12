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
| **core: changeset parse/render** | 7 | ✅ done (interop N/A; polyglot cleanup TODO) |
| **core: planner (cascade/group)** | 20 | ✅ done |
| **core: release/pre/snapshot planning** | 14 | ✅ done |
| **core: changelog rendering/formatter** | 43 | ⬜ **largest gap** (feature missing too) |
| **core: dotnet ecosystem (csproj)** | 13 | 🟡 (3 left: shared-props write, strategy ×2) |
| **core: node ecosystem (workspaces)** | 7 | ✅ done |
| **core: config / git / prestate / since** | 19 | 🟡 (since-consumers left, with A2) |
| **changerig: CLI commands** | 47 | ⬜ (parity harness covers version/pre/status e2e) |
| **relrig: release pipeline + publish** | 68 | ⬜ |
| **rig: config/jsonc/dotenv/discovery** | ~90 | ⬜ |
| **rig: verb logic / matching** | ~70 | 🟡 (cd done, rest TODO) |
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
- ⛔ `Cleanup_PureNodeMd_IsLeftUntouched` — N/A (but a polyglot "remove processed
      changeset" test is still needed — see TODO below)
- [ ] **NEW:** polyglot changeset cleanup (remove fully-consumed, keep partial)
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

### Changelog rendering & formatting — ⬜ **LARGEST GAP** (no Go tests)
Tracks [[changelog-formatter-native-design]] and [[changelog-generator-plugins-design]].
First confirm whether the Go code even exists yet, then test.

`Version/NativeMarkdownFormatterTests.cs` (18) → `core/...` formatter:
- [ ] `InsertsBlankLineBetweenVersionAndSectionHeadings`
- [ ] `StripsTrailingWhitespace`
- [ ] `CollapsesRunsOfBlankLines`
- [ ] `KeepsListItemsTight`
- [ ] `OneMultiParagraphItemMakesWholeSectionLoose`
- [ ] `LooseListSpacesBeforeNestedDependencySublist`
- [ ] `AlignsTableColumns`
- [ ] `AlignsTopLevelTableWithDefaultAlignment`
- [ ] `LeavesNonTableWithPipesUntouched`
- [ ] `FormatsAGnarlyChangeset`
- [ ] `EnsuresSingleTrailingNewline`
- [ ] `DropsLeadingBlankLines`
- [ ] `PreservesBlankLinesInsideCodeFences`
- [ ] `StripsTrailingWhitespaceInsideCodeFences`
- [ ] `SeparatesHeadingFromFollowingFence`
- [ ] `NormalizesCarriageReturns`
- [ ] `IsIdempotent`
- [ ] `DoesNotTreatIndentedHashAsHeading`

`Version/ChangelogFormatterTests.cs` (10) → formatter dispatch:
- [ ] `WhenDisabled_DoesNothing`
- [ ] `WithNoFiles_DoesNothing`
- [ ] `ExplicitOxfmt_RunsViaPackageManager`
- [ ] `Deno_RunsDenoFmtDirectly`
- [ ] `Auto_DetectsFormatterFromConfigFile`
- [ ] `Auto_NoFormatterConfigured_DoesNothing`
- [ ] `InPnpmRepo_UsesPnpmExec`
- [ ] `UnknownFormatter_DoesNothing`
- [ ] `WhenFormatterCannotStart_DegradesGracefully`
- [ ] `Native_RewritesInPlaceWithoutRunningProcess`

`Version/ChangelogFileWriterTests.cs` (3):
- [ ] `GeneratesNewFile_WhenAbsent`
- [ ] `AmendsExistingFile_WhenExists`
- [ ] `GeneratesTwoChangelogs_ForMultipleProjects`

`Version/ChangelogCommitResolverTests.cs` (5) — git/GitHub enrichment:
- [ ] `Default_DoesNotTouchGit_ReturnsEmpty`
- [ ] `Git_ResolvesShortCommitThatAddedChangeset`
- [ ] `Git_NoCommitFound_SkipsChangeset`
- [ ] `GitHub_ResolvesCommitPullRequestAndAuthor`
- [ ] `GitHub_WhenGhFails_KeepsCommitNullsPrAuthor`

`Version/ChangelogReleaseLineTests.cs` (7):
- [ ] `Default_ReturnsSummaryUnchanged`
- [ ] `Git_PrefixesShortCommit`
- [ ] `Git_WithoutCommit_Unchanged`
- [ ] `Git_OnlyPrefixesFirstLine`
- [ ] `GitHub_BuildsPrLinkCommitLinkAndThanks`
- [ ] `GitHub_WithoutPr_UsesCommitLinkOnly`
- [ ] `GitHub_WithoutData_Unchanged`

### dotnet ecosystem (csproj) — `CsProjectsRepositoryTests` + `CsProjectStrategyTests` + `ProjectVersionResolverTests` → `core/ecosystem/dotnet/dotnet_test.go`
- [x] `UpdateModuleCsProjs_IncreasesVersion` → `TestSetVersionInline`
- [x] `AddsInlineVersion_WhenNone` → `TestSetVersionInsertsWhenAbsent`
- [x] `WritesVersionPrefix_LeavesSuffix` → `TestSetVersionWritesPrefixLeavesSuffix`
- [ ] `WritesSharedPropsOnce_ForLockstepGroup` (Directory.Build.props lockstep write)
- [x] `GetCsProjects_OnlyValidVersion` → `TestDiscover`
- [ ] `CsProjectStrategy_ToIndependent_RedirectsSharedToOwnCsproj`
- [ ] `CsProjectStrategy_ToIndependent_LeavesInlineUntouched`
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

### config / git / prestate / since — 🟡 (tests landed; `since` consumers TODO)
`Shared/ConfigurationServiceTests.cs` (5) → `core/config/config_test.go`:
- [x] `ReadsSharedKeysAndNestedDotnet` → `TestParseSharedKeysAndEcosystemBlocks`
- [x] `ReadsFormatKey_AsBoolOrString` → `TestParseFormatBoolOrString`
- ⛔ `LegacyFlatKeys_MigratedToDotnet` — N/A (the Go config never had the C#
      tool's legacy flat keys; nothing to migrate)
- [x] `UnknownAndMissingKeys_ToleratedWithDefaults` → `TestParseUnknownAndMissingKeysTolerated`
      (+ `TestDefaults`)
- [~] `CreateDefault_WritesDualToolConfigThatRoundTrips` → `TestDefaults` covers the
      default values; the file-writing half belongs to the `init` command tests (A2)

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

`Shared/SinceChangesTests.cs` (2) — the `--since` consumers (changerig
`status --since` / `add --since`) don't exist in Go yet; the git substrate
(`ChangedFilesSince`) is now in place. Pair these with the A2 command tests:
- [ ] `ChangedProjectNames_ReturnsProjectsOwningChangedFile`
- [ ] `AnyChangesetAdded_DetectsChangesetFiles_NotReadme`

## A2. `changerig` — changeset CLI commands (all ⬜)

These are command-level tests (C# uses Spectre `CommandAppTester`). Go equivalent
= `testscript`/`.txtar` end-to-end or cobra command tests. None exist yet.

- **Add** (`AddCommandTests`, 9): happy path, `--since` preselect, `--empty`,
  `--open`/no-open, `-m` message, not-initialized error.
  ⛔ the two interop-extension cases are N/A.
- **Init** (`InitCommandTests`, 4): creates folder+config, re-run regenerates
  missing config, "already exists" message, interactive config write.
- **Info** (`InfoChangesetCommandTests`, 7): renders config+project count,
  pending-changeset breakdown, skipped-no-version, before-init error.
  ⛔ the three Node-probe cases are N/A.
- **Pre** (`PreChangesetCommandTests`, 6): enter writes pre-mode+versions,
  already-in-pre fails, no-tag fails, exit flips mode, exit-when-not-pre fails,
  unknown-action fails. [~] the enter/exit + version lifecycle is now covered
  end-to-end by parity `TestPrereleaseParity`; the error-path unit cases TODO.
- **Status** (`StatusChangesetCommandTests`, 10): changeset present/absent exit
  codes, `--since`, `--verbose`, bump-level grouping. [~] `--output` JSON plan now
  implemented + covered by parity `TestStatusPlan`; the rest TODO.
  ⛔ the three Node-delegation cases are N/A.
- **Tag** (`TagChangesetCommandTests`, 4): tag per project at version, skip
  existing, honor ignore, use PackageId. *(shared with relrig `tag`)*
- **Ui** (`UiChangesetCommandTests`, 2): runs selected command, non-interactive guard.
- **Version** (`VersionChangesetCommandTests`, 3): [~] happy path *(exercised
  end-to-end by parity `TestParity`)*; [x] snapshot zero-version + changeset
  consumption → parity `TestSnapshotParity`; pre-mode writes prerelease +
  records id → parity `TestPrereleaseParity`.
- **Dispatcher** (`CommandDispatcherTests`, 1): nested dispatch + exit-code propagation.
- ⛔ `ProcessExecutorTests` (2), `NodeChangesetServiceTests` (3) — N/A.

## A3. `relrig` — release orchestrator (all ⬜)

Tracks [[release-command-design]]. Largely deferred design in Go; no tests yet.

- **ReleasePipeline** (`ReleasePipelineTests`, 28): step resolution (default
  order, builtin version/publish delegation, custom run steps, unknown-step
  errors), `--only`/`--skip`/`--from`/`--to` filtering, disabled-step skip
  reasons, before/action/after hook ordering, on-error + skip-later, `${tool}`
  interpolation + shell dispatch, lazy/eager var capture + secret masking,
  dry-run no-op, confirm gate (approve/decline), native step handler invoke/fail/skip.
- **ReleaseConfigService** (6): empty-on-missing, JSONC comments+trailing commas,
  command shapes (shell/argv), hooks+vars, confirm as bool-or-string, invalid-JSON throws.
- **ReleaseUiMode** (6): terminal defaults rich+interactive, redirected→plain,
  `--ui` rich-not-interactive-when-piped, `--no-ui` wins, `--yes` disables
  interactivity, redirected input disables interactivity.
- **TuiReleaseReporter** (4) + **PlainReleaseReporter** (2): plan/step rendering,
  resume hint on failure, success panel/no-hint, secret masking.
- **PlanChooser** (1): passthrough.
- **ForgeReleaseService** (6): none-mode no-op, github create-for-missing-tag,
  skip-existing, non-github origin skip, auto-mode+gh-ready creates, changelog
  section as release notes.
- **Publish** (`PublishChangesetCommandTests` 9 + `DotnetServiceTests` 2 +
  `NuGetPackageRegistryTests` 4): pack+publish happy path, auto-tag/no-tag,
  no-project/all-published early exits, selective publish by feed state,
  PackageId feed queries, push `--skip-duplicate`, empty-source throws,
  nuget.org lowercase id, 404→empty, non-http→null, V3 base-address resolution.
  ⛔ the interop "auto-run node publish" case is N/A.

---

# Source B — rig (.NET) → `rig` (Go)

Go `rig` tests live in `cli/internal/...`. Today: `cd` (done), partial
`coverage`/`doctor`/`kill`/discovery; everything else TODO.

### Matching / navigation
`CdTests.cs` (5) → `cli/internal/cli/cd_test.go`:
- [x] all 5 (exact-short-name, no-match, path-basename, subsequence, name-outranks-path)
      → Go has a **superset** (`TestRank*` ×13).

`GlobTests.cs` (1 method / 9 cases) → glob matcher:
- [~] anchored `*`/`?` case-insensitive matching → partially via
      `walkutil` `TestIgnorer`; add a dedicated glob test with these 9 cases.

`PrefixResolverTests.cs` (7) → ⬜ (verb prefix expansion, watch-modifier):
- [ ] unambiguous-prefix-rewritten, ambiguous-left-alone, exact/options
      passthrough, unknown-token passthrough, `ExpandWatch` flag, bare-watch,
      longer-unambiguous-prefix.

### Discovery / root resolution
`ProjectDiscoveryTests.cs` (7) → `cli/internal/detect/detect_test.go`:
- [~] nearest-ecosystem detection done (`TestNearestEcosystem_*` ×5), but the
      .NET-specific cases are TODO: classify-from-slnx, exclude-globs,
      IsExcluded name/path, classic-sln parse, *Tests convention, csproj
      fallback, configured-solution override.

`RootResolverTests.cs` (6) → ⬜ (anchor: rig.json > solution > git root):
- [ ] rig.json wins over closer solution, solution fallback, git-root fallback,
      git boundary stops climb, in-repo solution beats git root, .git-file worktree.

`CapabilitiesTests.cs` (4) → ⬜:
- [ ] run/publish gated on runnable, test/coverage gated on test project,
      build/kill/custom always available, empty-dir reports nothing.

### Config / JSONC / env
`RigConfigTests.cs` (15) → `cli/internal/config` (⬜, no tests):
- [ ] unknown-key suggestion, known-keys-no-warning, merge (repo wins + dict
      union), exclude-list union + quiet precedence, blank-license falls through,
      malformed degrades to defaults, missing→defaults, full JSONC schema parse,
      dotnet-namespace fold, dotnet beats legacy top-level, node-namespace
      ignored, command string/array/object forms, per-OS override.

`JsoncEditorTests.cs` (13) → ⬜ (comment-preserving JSONC edit):
- [ ] replace-preserving-comments, insert-keeping-members, no-nested-name-match,
      insert-empty-object, unicode byte-offsets, malformed→false,
      nested replace/insert, create-parent, create-in-empty-root,
      refuse-non-object-parent, inline single-line insert, BOM tolerance.

`ConfigWriterTests.cs` (5) + `ConventionTests.cs` (11) → ⬜:
- [ ] fresh-file with schema+nested, splice subsequent writes, refuse-clobber,
      whitespace-only fresh, SetString returns repo path; ConfigWriter
      create/update/preserve-comments, default-setter persists/rejects, rebuild
      scoping + dry-run, slnx-before-sln, runsettings discovery (single/ambiguous/none).

`DotEnvTests.cs` (8) + `EnvStackTests.cs` (2) → ⬜:
- [ ] quoted-escapes, basic pairs+comments, strip-export, quote-literal rules,
      inline-comment-unquoted-only, skip-invalid-keys, `.env.local` overlay,
      empty-when-none; env precedence (file>ambient>config>command), null-layers-ignored.

### Coverage / doctor
`CoverageTests.cs` (8) → `cli/internal/cli/coverage_test.go`:
- [~] Go has `TestParseLinePct`, `TestParseGoCoverage` (parsing only). TODO:
      MeetsMinimum gate, ResolveOptions CLI-over-config, runner explicit/auto-mtp,
      collect-args by-runner + filter-when-scoped, cobertura ReadRates,
      in-process HTML render.

`DoctorTests.cs` (5) → `cli/internal/cli/doctor_test.go`:
- [x] `SdkSatisfies` same/newer + older → `TestSdkSatisfies`, `TestMajorOf`
- [ ] `SdkSatisfies_defersWhenPinAbsent`, `ReadSdkPin_returnsPinOrNull`,
      `ReadSdkPin_findsGlobalJsonInAncestor`

### Verb logic — `VerbLogicTests.cs` (34) → mostly ⬜
- [~] **Kill** (8 of the 34) → `cli/internal/cli/kill_test.go` covers PID/netstat
      parsing, name-match, self-filter (6 tests). Confirm: prefers-configured-match,
      bare-kill-sweeps-all, kill-named, project-name-not-assembly, lsof parsing.
- [ ] **Run/Test** (10): sole-runnable, prefers-default, ambiguous, query match,
      no-runnable error, run-arg ordering (framework/launch-profile before `--`),
      omit-unset+prepend-watch, test framework+filter, vstest-positional, mtp-`--project`.
- [ ] **Rebuild** (2): skip exact/prefix segments, within-root delete guard.
- [ ] **Publish** (2): rid/output defaults+overrides, self-contained+single-file args.
- [ ] **Add/Remove/Global/Dlx** (5): add target resolution, `dotnet remove package`,
      `tool install -g`, `dnx` tool-first, dnx availability check.
- [ ] **Update/Outdated** (5): latest-stable-ignores-prerelease, is-newer compare,
      sibling self-only, outdated lens defaults, lens mutual-exclusivity.
- [ ] **Win exec** (1): cmd.exe caret-escaping.
- [ ] **Test/build config** (1): configuration passing.

### Other rig tests
- `InfoInitTests.cs` (4) → ⬜: init template, refuse-overwrite, info-on-empty,
  coverage-defaults summary.
- `MenuInputTests.cs` (4) → ⬜: escape/backspace cancel, passthrough, async path.
- `IntegrationTests.cs` (6) → ⬜: real `dotnet` build E2E, custom shell/argv
  command exit codes + arg forwarding + env + missing-os-spec error.
- `TestEnumerationTests.cs` (6) → ⬜: detect mstest/nunit/xunit, plain-class-not-test,
  real-assembly enumeration, cross-tfm metadata gate.
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
- [ ] dotnet ecosystem corpus (goldens cross-generated from net-changesets);
      cross-ecosystem polyglot cascade.
- [ ] (optional) Point the C# parity tests at this same corpus so one golden set
      is authoritative for both implementations.

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

---

## How to use this file
1. Pick a section, implement/locate the Go test, flip `[ ]`/`[~]` → `[x]`.
2. Resolve every `[~]` "confirm" note by reading the Go test — either upgrade to
   `[x]` or split out the missing case as a fresh `[ ]`.
3. Priority order: **changelog rendering/formatter (A2)** → **config/git/prestate
   (A1)** → **parity corpus** → command/pipeline tests → rig config/jsonc/env.
