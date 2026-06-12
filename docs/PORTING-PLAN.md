# rigsmith porting plan

> **Historical (2026-06-11).** This was the original staged plan; the port is
> complete as of 2026-06-12. Current state lives in
> [FEATURE-PARITY.md](FEATURE-PARITY.md) (feature surface) and
> [../test-parity.md](../test-parity.md) (test coverage). Status markers below
> are NOT maintained.

Two source projects → two Go binaries on a shared core, at feature parity.

- **net-changesets** (C#, ~7k LOC) → `relrig`
- **rig** (.NET ~7.3k LOC + Node ~3.1k LOC, kept at parity) → `rig`

Legend: ✅ done · 🟡 partial/scaffolded · ⬜ not started · ➖ n/a

## relrig (from net-changesets)

| Area | Source | Status | Notes |
|---|---|---|---|
| Semver + bump rules | `Shared/Semver.cs` | ✅ | `core/semver`, unit-tested incl. prerelease graduation |
| Changeset parse/render | `ChangesetsRepository.cs` | ✅ | `core/changeset`, round-trip tested |
| Config schema | `ChangesetConfig.cs` | ✅ | `core/config`; ecosystem blocks generalized to a map |
| Dependency cascade | `ChangelogGenerator.cs` | ✅ | `core/planner`; rangeless (always-patch) case, tested transitively |
| Linked / fixed / lockstep grouping | `ChangelogGenerator.cs` | ✅ | `core/planner` |
| Changelog entry rendering | `ChangelogFileWriter.cs` | ✅ | `core/planner.RenderEntry` |
| `init` | `Init/` | ✅ | writes `.changeset/config.json` + README |
| `add` | `Add/` | 🟡 | flags + interactive (huh); no `--since`/`--open`/`--empty`-prompt yet |
| `status` | `Status/` | 🟡 | plan printout + `--verbose`; no `--output` JSON / `--since` |
| `version` | `Version/` | ✅ | plan + range-aware cascade + set-version + dep-range rewrites + changelog + delete; normal / snapshot / pre / exit modes |
| `info` | `Info/` | ✅ | config + ecosystems + discovered packages + changeset count |
| `publish` | `Publish/` | ✅ | registry publish per ecosystem (idempotent) + tag + push; `--dry-run`/`--no-git-tag`/`--no-push` |
| `tag` | `Tag/` | ✅ | git tags per package (Go module-path / `name@version`); skips existing |
| `pre enter/exit` | `Pre/` | ✅ | `.changeset/pre.json` state; `enter <tag>` / `exit`; graduation on next version |
| Snapshot releases | `ReleaseVersionPlanner.cs` | ✅ | `--snapshot[=tag]` + `--snapshot-template` ({tag}/{commit}/{datetime}/{timestamp}) |
| Changelog generators (git/github) | `ChangelogReleaseLine.cs` | ⬜ | commit/PR/author enrichment; needs git+gh |
| Changelog generator **plugins** | design doc | 🟡 | contract + host exist; built-in not yet routed through `ChangelogRequest` (dogfood step) |
| `ui` interactive menu | `Ui/` | ⬜ | bubbletea menu |
| `shell-init` | `ShellInit/` | ➖ | obviated — single binary on PATH (aliases `changeset`); cobra `completion` covers tab-completion, so no resolve-the-binary wrapper is needed |
| Native markdown formatter | `NativeMarkdownFormatter.cs` | ⬜ | port the prettier-compatible formatter |
| Node interop mode | `NodeChangesetService.cs` | ➖ | superseded by native node adapter (north-star); keep format-level coexistence |

## relrig — ecosystem engine (the north-star differentiator)

| Workstream | Status | Notes |
|---|---|---|
| .NET adapter (discover/set-version) | 🟡 | `ecosystem/dotnet`; publish stubbed |
| Node adapter (discover/set-version) | 🟡 | `ecosystem/node`; **range-aware cascade not yet done** (see below); publish stubbed |
| Go adapter | 🟡 | `ecosystem/gomod`; version model is a placeholder (see Questions) |
| Range-aware cascade (npm `^`/`~`/`workspace:`) | ✅ | `semver.Satisfies` + planner cascade: out-of-range gating, `updateInternalDependencies` threshold, peer→major, dev no-release, manifest range rewrites |
| Publish delegation | ✅ | `dotnet pack`/`nuget push --skip-duplicate`, `npm publish` (idempotent via `npm view`), `cargo publish`, Go git-tag push |

## rig (from rig .NET + Node)

| Verb / feature | Status | Notes |
|---|---|---|
| ecosystem detection + discovery | ✅ | reuses `core/ecosystem` |
| `build` / `test` / `run` / `format` | 🟡 | convention-default native command per ecosystem; `--dry-run` |
| `info` | 🟡 | root + primary ecosystem + discovered projects |
| `coverage` | ⬜ | .NET ReportGenerator / vitest-c8; the heaviest verb |
| `kill` (proc/port) | ⬜ | |
| `add` / `remove` / `install` / `upgrade` / `outdated` | ⬜ | package management per ecosystem (ni-parity) |
| `clean` / `rebuild` | ⬜ | |
| `publish` (dotnet) | ⬜ | |
| `--all` graph run (dep order) | ⬜ | topological run across workspace |
| interactive menu | ⬜ | bubbletea |
| `cd` to project | ⬜ | needs shell wrapper |
| fuzzy matching | ⬜ | test-class / project fuzzy match |
| shell completion (`[suggest]`) | 🟡 | cobra provides basic completion; the cross-tool `[suggest]` protocol is a port decision |
| `.rig.json` (JSONC, comment-safe) + global `~/.rig.json` | ⬜ | comment-preserving writes |
| env presets · `.env` loading | ⬜ | |
| `default` project · `init` · `setup` · `doctor` · `self-update` | ⬜ | |
| custom commands / scripts→verbs | ⬜ | `.rig.json commands`, npm-script surfacing |
| dev-loop verbs as ecosystem-plugin methods | ⬜ | extend the ecosystem contract so adapters declare build/test/run commands (instead of the hardcoded table in `internal/detect`) — unifies rig over the same plugin system |

## Suggested order

0. **Release loop: DONE** — range-aware cascade ✅, snapshot/pre modes ✅, publish
   delegation ✅, tagging ✅. The core `add → version → publish → tag` loop is complete.
1. **Release orchestrator** (`relrig release` + `.changeset/release.jsonc`) — the
   configurable pipeline (version→commit→publish→push→githubRelease, hooks, custom
   steps, lazy vars, forge releases). Designed in
   [RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md); **on hold** pending greenlight.
2. **Dogfood the changelog contract**: route the built-in renderer through
   `ChangelogRequest`; add git/github enrichment; ship one external reference
   generator as a conformance test.
3. **Native markdown formatter** (removes the last reason to want prettier).
4. **rig parity, ecosystem-by-ecosystem**, promoting the dev-loop command table
   into the ecosystem-plugin contract so rig and external adapters share it.
5. **`.rig.json` + global config + completion + menu** (the ergonomics layer).
6. **Distribution**: GoReleaser → curl|sh, Homebrew, Scoop, GitHub Releases, and
   the npm wrapper (binary-launcher pattern), per memory `project-north-star`.
