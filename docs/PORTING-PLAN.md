# rigsmith porting plan

> **Status: port complete.** This was the original staged plan (2026-06-11).
> The markers below were re-verified against the code on 2026-06-12 and now
> record the final state. For the living, detailed audit see
> [FEATURE-PARITY.md](FEATURE-PARITY.md) (feature surface) and
> [../test-parity.md](../test-parity.md) (test coverage).

Two source projects → two Go binaries on a shared core, at feature parity.

- **net-changesets** (C#, ~7k LOC) → `changerig` / `shiprig`
- **rig** (.NET ~7.3k LOC + Node ~3.1k LOC, kept at parity) → `rig`

Legend: ✅ done · ⬜ not started · ➖ n/a

## changeRig / shipRig (from net-changesets)

| Area | Source | Status | Notes |
|---|---|---|---|
| Semver + bump rules | `Shared/Semver.cs` | ✅ | `core/semver`, unit-tested incl. prerelease graduation |
| Changeset parse/render | `ChangesetsRepository.cs` | ✅ | `core/changeset`, round-trip tested |
| Config schema | `ChangesetConfig.cs` | ✅ | `core/config`; full surface incl. `commit`, `versionStrategy`, and the generalized per-ecosystem block (`sourcePath`/`packageSource`/`versionStrategy`) |
| Dependency cascade | `ChangelogGenerator.cs` | ✅ | `core/planner`; rangeless (always-patch) case, tested transitively |
| Linked / fixed / lockstep grouping | `ChangelogGenerator.cs` | ✅ | `core/planner` |
| Changelog entry rendering | `ChangelogFileWriter.cs` | ✅ | `core/planner.RenderEntry` |
| `init` | `Init/` | ✅ | writes `.changeset/config.json` + README |
| `add` | `Add/` | ✅ | flags + interactive (huh); `--since` (picker preselect), `--open` ($EDITOR), `--empty`, plus `--type`/`--bump` beyond the source |
| `status` | `Status/` | ✅ | `--verbose`, `--since` (changed-without-changeset guard), `--output` JSON plan, pre-mode reflection |
| `version` | `Version/` | ✅ | plan + range-aware cascade + set-version + dep-range rewrites + changelog + delete; normal / snapshot / pre / exit modes; `--independent` |
| `info` | `Info/` | ✅ | config + ecosystems + discovered packages + changeset count |
| `publish` | `Publish/` | ✅ | registry publish per ecosystem (idempotent) + tag + push; `--dry-run`/`--no-git-tag`/`--no-push`/`--access` + TTY confirm gate |
| `tag` | `Tag/` | ✅ | git tags per package (Go module-path / `name@version`); skips existing |
| `pre enter/exit` | `Pre/` | ✅ | `.changeset/pre.json` state; `enter <tag>` / `exit`; graduation on next version |
| Snapshot releases | `ReleaseVersionPlanner.cs` | ✅ | `--snapshot[=tag]` + `--snapshot-template` ({tag}/{commit}/{datetime}/{timestamp}) |
| Changelog generators (git/github) | `ChangelogReleaseLine.cs` | ✅ | `core/changelog`: commit-hash prefix via git log; PR/author/Thanks links via `gh api`, degrading gracefully |
| Changelog generator **plugins** | design doc | ✅ | `ChangelogRequest` subprocess contract, implemented (net only designed it); built-in dogfoods it; Node `changelogen` reference plugin |
| `release` orchestrator | (new) | ✅ | `shiprig release` + `.changeset/release.jsonc`: steps/hooks/lazy vars/confirm gates/forge releases — see [RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md). Remaining tail: interactive step-chooser TUI (passthrough today), `packages.versionRegex` |
| `ui` interactive menu | `Ui/` | ✅ | bubbletea menu dispatching the verbs |
| `shell-init` | `ShellInit/` | ➖ | obviated — single binary on PATH (aliases `changeset`); cobra `completion` covers tab-completion |
| Native markdown formatter | `NativeMarkdownFormatter.cs` | ✅ | `core/mdfmt`: prettier-equivalent port (18 golden tests, idempotent) + `format:` dispatch incl. custom-argv escape hatch |
| Node interop mode | `NodeChangesetService.cs` | ➖ | superseded by the native node adapter; no `.net.mkd` dual-readability (deliberate, no .NET back-compat) |

## ecosystem engine (the north-star differentiator)

| Workstream | Status | Notes |
|---|---|---|
| .NET adapter (discover/set-version/publish) | ✅ | `ecosystem/dotnet`; inline vs Directory.Build.props, format-preserving writes, slnx/sln discovery |
| Node adapter (discover/set-version/publish) | ✅ | `ecosystem/node`; workspaces (pnpm/yarn/npm/bun) |
| Go adapter | ✅ | `ecosystem/gomod`; git-tag versioning (module-path tags) |
| Cargo adapter | ✅ | `ecosystem/cargo` (beyond the original plan) |
| Range-aware cascade (npm `^`/`~`/`workspace:`) | ✅ | `semver.Satisfies` + planner cascade: out-of-range gating, `updateInternalDependencies` threshold, peer→major, dev no-release, manifest range rewrites |
| Publish delegation | ✅ | `dotnet pack`/`nuget push --skip-duplicate`, `npm publish` (idempotent via `npm view`), `cargo publish`, Go git-tag push |
| Dev-loop verbs in the plugin contract | ✅ | `EcosystemInfo.DevCommands` (`core/plugin/protocol.go`) — adapters declare build/test/run/… themselves; the hardcoded table in `internal/detect` is gone |

## rig (from rig .NET + Node)

| Verb / feature | Status | Notes |
|---|---|---|
| ecosystem detection + discovery | ✅ | reuses `core/ecosystem`; nearest-manifest primary + `ecosystem` pin for polyglot repos |
| `build` / `test` / `run` / `format` / `lint` / `typecheck` | ✅ | via each adapter's `DevCommands`; `--dry-run`/`--quiet`; project scoping + verb-prefix |
| `info` | ✅ | root, primary ecosystem, `.rig.json`, command mappings, packages (exclude-filtered) |
| `coverage` | ✅ | ReportGenerator-first for all ecosystems (Cobertura/lcov/go-profile conversion) with native fallback; `--min` gate, `--open`, vitest reporter auto-injection, TTY download offer |
| `kill` (proc/port) | ✅ | `--port` (lsof/netstat), name/pattern (pgrep/pkill · taskkill/CIM), `kill.match` config, `--dry-run` |
| `add` / `uninstall` / `install` / `ci` / `upgrade` / `outdated` | ✅ | per-ecosystem native; node pm-detected (ni-parity); aliases |
| `global` / `dlx` | ✅ | `dotnet tool -g`/`dnx`, `go install`, `cargo install`, pm-aware node (`pnpm dlx` …) |
| `clean` / `rebuild` | ✅ | native per ecosystem; `rebuild` = clean→build; node maps to the project's `clean` script |
| `publish` (dotnet) | ✅ | rid/output/configuration/self-contained/single-file; flag > `.rig.json publish.*` > default; `{rid}` templating |
| `--all` graph run (dep order) | ✅ | topo sort across the polyglot workspace, cycle-tolerant; `--filter <glob>` |
| interactive menu | ✅ | grouped bubbletea menu, project picker, focus scoping, capability probing |
| `cd` to project | ✅ | tiered fuzzy match + TTY picker; `rig setup` installs the shell wrapper (zsh/bash/fish) |
| fuzzy matching | ✅ | project short-name/substring/subsequence; test-class fuzzy → C# `--filter` shapes; `--list-tests` enumeration (VSTest/MTP) |
| shell completion | ✅ | cobra `__complete` + dynamic project/runnable completion; bespoke `[suggest]` protocol obviated |
| `.rig.json` (JSONC) + global `~/.rig.json` | ✅ | merge, namespaces, per-OS commands, did-you-mean warnings, comment-preserving writes (`core/jsonc`) |
| `.env` / `.env.local` loading | ✅ | `cli/internal/envstack`, exact C# quoting + precedence, wired into every spawn path |
| env presets as flags | ✅ | `presets.go`: one bool flag per `.rig.json` preset, merged as the top env layer |
| `--no-env` / `--root` | ✅ | persistent root flags: skip `.env` loading / override walk-up root resolution |
| `default` · `init` · `setup` · `doctor` · `self-update` | ✅ | `setup` goes further than the C# (installs cd wrapper + completion into rc files); `self-update` checks GitHub releases vs the ldflags-stamped version |
| custom commands / scripts→verbs | ✅ | `.rig.json commands` (string/argv/object, per-OS, env, cwd); package.json scripts become verbs |
| `watch` modifier | ✅ | `rig watch <verb>` / `rig w r` / position-independent `--watch` on run/build/test |

## Remaining work

1. **shipRig tail** — interactive step-chooser TUI for `release` (passthrough
   today) and `packages.versionRegex`.
2. **Distribution tail** — GitHub Releases + `curl|sh` are live; Homebrew tap,
   Scoop bucket, and the npm binary wrapper remain
   (see [DISTRIBUTION.md](DISTRIBUTION.md)).
3. **Optional ergonomics** — the C#-style interactive config walkthrough
   (covered today by `init` + `default`), Vite dev-server detection.
