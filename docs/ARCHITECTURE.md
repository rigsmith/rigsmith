# rigsmith architecture

## The shape

```
                ┌───────────────────────────┐
                │  rigsmith/core (no deps)   │
                │                            │
   rig  ───────▶│  ecosystem registry  ◀────│───────  shiprig
 (dev launcher) │  + detection/discovery     │       (release tool)
                │                            │
                │  semver · changeset ·      │
                │  config · planner ·        │
                │  plugin contract           │
                └───────────────────────────┘
```

`core` is the only shared dependency. It deliberately has **zero external Go
dependencies** (stdlib only) so it stays the stable, portable, fast-to-build
heart of both tools. The charm/cobra UI stack lives only in the two CLI modules.

### Why a shared core (not two copies)

`rig` and `shiprig` both need to answer "what kind of repo is this, and what
packages/projects does it contain?" That question — ecosystem detection plus
package discovery — is the same in both tools, and getting it to disagree
between them would be a bug. So it lives once in `core/ecosystem`, behind the
`plugin.Ecosystem` interface, and both binaries consume the same registry.

- **`shiprig`** uses an adapter's `Discover` / `SetVersion` / `Publish` to run the
  release workflow.
- **`rig`** uses the same `Detect` / `Discover` to know which native dev-loop
  command to run (`go build` vs `dotnet build` vs `npm run build`).

The release **engine** (cascade, grouping, changelog) is `shiprig`-only but also
lives in `core` (`core/planner`) because it is pure, ecosystem-agnostic logic —
the same property that made it portable out of C# in the first place.

## Modules

### `core/`

| Package | Responsibility | Ported from |
|---|---|---|
| `semver` | SemVer 2.0.0 value + node-semver bump rules (major/minor/patch with prerelease graduation) | `Shared/Semver.cs` |
| `changeset` | Parse/render `.changeset/*.md` (shared @changesets frontmatter format) | `Shared/ChangesetsRepository.cs`, `ChangesetFile.cs` |
| `config` | The unified `.changeset/config.json` schema; ecosystem blocks kept raw for per-adapter decoding | `Shared/ChangesetConfig.cs`, `DotnetConfig.cs` |
| `planner` | The release plan: per-package version, the dependency **cascade**, **linked/fixed/lockstep** grouping, and the changelog entry renderer | `Commands/Version/Helpers/ChangelogGenerator.cs`, `ChangelogFileWriter.cs`, `ModuleChangelog.cs` |
| `plugin` | The extension contract (interfaces + JSON protocol + subprocess host + registry) for both ecosystem adapters and changelog generators | `docs/changelog-generator-plugins-design.md` |
| `ecosystem/{dotnet,node,gomod,cargo}` | Built-in language adapters — in-process implementations of `plugin.Ecosystem` | `Commands/Version/Helpers/CsProjectsRepository.cs` (+ new node/go/cargo) |
| `changelog` | changelog-git/-github enrichment (commit/PR/author), the release-line decorator, and the CHANGELOG file writer | `Version/Helpers/ChangelogCommitResolver.cs`, `ChangelogReleaseLine.cs`, `ChangelogFileWriter.cs` |
| `mdfmt` | the native prettier-equivalent markdown formatter + the `format:` dispatcher (auto-detect, package-manager exec, custom argv) | `Version/Helpers/NativeMarkdownFormatter.cs`, `ChangelogFormatter.cs` |
| `jsonc` | tolerant JSONC parse (offset-preserving) + the comment-preserving editor | rig's `JsoncEditor.cs` |
| `gitutil` / `prestate` / `since` / `walkutil` | git tags + merge-base diffs, `.changeset/pre.json`, changed-files→projects mapping, ignore-aware tree walking | `Shared/GitService.cs`, `PreStateRepository.cs`, `SinceChanges.cs` |

### `cli/` → `rig`

`internal/detect` reuses `core/ecosystem` for detection, resolves the repo
root with the rig precedence (`.rig.json` > solution/manifest > git root), and
carries the .NET project discovery (slnx/sln, test classification,
capabilities). `internal/config` is the JSONC `.rig.json` (merge, namespaces,
rich per-OS commands, comment-preserving writes); `internal/envstack` layers
`.env`/`.env.local` < ambient < config < command into every spawn.
`internal/cli` is the cobra+fang command tree: the dev loop, package
management, `coverage`/`kill`/`doctor`/`cd`/`rebuild`/`publish`, scripts→verbs,
the verb-prefix + watch pre-parse pipeline, `--all` topo runs, and the
capability-gated menu. Remaining ergonomics tail is listed in
FEATURE-PARITY.md.

### `shiprig/` → `shiprig`

`internal/app` resolves the workspace (repo root, `.changeset`, config,
registry, discovery). `internal/cli` is the cobra+fang command tree: the full
changeset surface (`init`, `add`, `status`, `version`, `pre`, `info`, `ui`)
plus `publish` (confirm-gated), `tag`, and `release` — the configurable step
pipeline in `internal/pipeline` (steps/hooks/vars/gates/secret-masking) with
forge (GitHub) releases in `internal/forge`.

## The north-star property

Everything above serves one goal (memory `project-north-star`): **one uniform
`add changeset → version → publish` process across every ecosystem, shipped as a
single zero-runtime binary.** The engine runs natively and identically
everywhere; only the genuinely ecosystem-specific steps (publish, workspace-graph
resolution) delegate — and they delegate to the **package manager** (`dotnet`,
`npm`, `cargo`), never to a peer workflow tool like `@changesets`. That boundary
is what keeps "one process" true where there are the most repos.
