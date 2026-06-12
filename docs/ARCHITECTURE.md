# rigsmith architecture

## The shape

```
                ┌───────────────────────────┐
                │  rigsmith/core (no deps)   │
                │                            │
   rig  ───────▶│  ecosystem registry  ◀────│───────  relrig
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

`rig` and `relrig` both need to answer "what kind of repo is this, and what
packages/projects does it contain?" That question — ecosystem detection plus
package discovery — is the same in both tools, and getting it to disagree
between them would be a bug. So it lives once in `core/ecosystem`, behind the
`plugin.Ecosystem` interface, and both binaries consume the same registry.

- **`relrig`** uses an adapter's `Discover` / `SetVersion` / `Publish` to run the
  release workflow.
- **`rig`** uses the same `Detect` / `Discover` to know which native dev-loop
  command to run (`go build` vs `dotnet build` vs `npm run build`).

The release **engine** (cascade, grouping, changelog) is `relrig`-only but also
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
| `ecosystem/{dotnet,node,gomod}` | Built-in language adapters — in-process implementations of `plugin.Ecosystem` | `Commands/Version/Helpers/CsProjectsRepository.cs` (+ new node/go) |

### `cli/` → `rig`

`internal/detect` reuses `core/ecosystem` for detection and maps rig verbs to
native commands. `internal/cli` is the cobra+fang command tree. Today: `build`,
`test`, `run`, `format`, `info`, with `--dry-run`. The long tail (coverage,
kill, publish, menu, completion, cross-ecosystem delegation, `.rig.json`) is
scaffolded in PORTING-PLAN.md, not yet built.

### `release/` → `relrig`

`internal/app` resolves the workspace (repo root, `.changeset`, config,
registry, discovery). `internal/cli` is the cobra+fang command tree: `init`,
`add`, `status`, `version`, `info` wired; `publish`, `tag`, `pre` scaffolded.

## The north-star property

Everything above serves one goal (memory `project-north-star`): **one uniform
`add changeset → version → publish` process across every ecosystem, shipped as a
single zero-runtime binary.** The engine runs natively and identically
everywhere; only the genuinely ecosystem-specific steps (publish, workspace-graph
resolution) delegate — and they delegate to the **package manager** (`dotnet`,
`npm`, `cargo`), never to a peer workflow tool like `@changesets`. That boundary
is what keeps "one process" true where there are the most repos.
