# Feature parity audit — rigsmith vs the source tools

Audit of the Go rigsmith implementation against its two source projects
(updated 2026-06-12 after parity phases 1–6; originally 2026-06-11):

- **net-changesets** (.NET) → `changerig` / `relrig` + `core`
- **rig** (.NET + Node, kept at parity) → `rig` (the `cli/` module) + `core`

> Companion docs: [test-parity.md](../test-parity.md) tracks *test* coverage
> per C# suite; this file tracks the *feature* surface. Behavior is pinned by
> the parity corpus (`core/testdata/parity/`, 22 scenarios, Node + C# oracles).

**Legend:** ✅ done · 🟢 done + exceeds the source · 🟡 partial · ⬜ not yet · ➖ n/a (intentionally out of scope).

> **No .NET back-compat** ([[rigsmith-no-dotnet-compat]]): items marked ➖ "dropped"
> are deliberate — rigsmith does not preserve net-changesets' Node-interop bridge,
> its `dotnet`-block config fidelity, or `.changeset/*.md` dual-tool readability.

## Headline

- **Release engine (changerig/relrig): at or above parity.** The full
  `init → add → status → version → publish → tag → release` loop works across
  **four** ecosystems (net-changesets had one), with a **range-aware cascade**,
  an **implemented pluggable-changelog system** that net-changesets only
  designed, the **`release` orchestrator** (steps/hooks/vars/gates/forge),
  **changelog git/github enrichment**, and the **markdown formatter**
  (`format:` incl. the native prettier-equivalent and a 🟢 custom-command
  escape hatch). Remaining changerig tail: `--independent`, `commit` config
  key, `shell-init`.
- **`rig` (dev launcher): high (Phase 6, 2026-06-12).** Dev loop + full package
  management + `coverage` (incl. .NET `--min` gate + in-process cobertura HTML)
  + `kill` (C#-aligned semantics) + `doctor`/`cd`/`init`/`rebuild`/`publish`/
  `global`/`dlx` + node scripts→verbs + **`--all` topo graph + `--filter` +
  project scoping + verb-prefix + watch-modifier pipeline** + capability-probed
  menu + **JSONC `.rig.json`** (merge, namespaces, rich per-OS commands,
  did-you-mean warnings, comment-preserving writes) + **`.env`/`.env.local`
  layering** + C#-precedence root resolution + full .NET project discovery
  (slnx/sln). Remaining tail: `[suggest]` completion, menu project-pickers,
  `setup`/`self-update`, the interactive `default` verb, test-class fuzzy,
  per-verb `--watch` flag, Windows CIM kill. One architectural win:
  **no cross-tool delegation needed** — the single Go binary handles every
  ecosystem natively (the .NET/Node rig split exists only because neither
  could).

---

# Part A — changerig / relrig vs net-changesets

## Commands

| Feature | net-changesets | rigsmith | Notes |
|---|---|---|---|
| `init` | ✅ | 🟡 | Creates `.changeset/` + `config.json` + README. Interactive sourcePath/packageSource/interop prompts ⬜; exit-code taxonomy (1/2) ⬜ (simple "already initialized"). |
| `add` (default) | ✅ | 🟡 | `-m/--message` ✅, `-p/--package` ✅, `--empty` ✅, `--since` (picker preselect) ✅. **🟢 `--type`/`-t`** (conventional) + **`--bump`** + **omittable bump**. `--open` (editor) ⬜. human-id filename ✅. |
| `version` | ✅ | 🟡 | normal/snapshot/pre/exit modes ✅. `--snapshot[=tag]` ✅, snapshot template ✅ (flag named `--snapshot-template` vs net's `--snapshot-prerelease-template`). changelog enrichment + `format:` pass wired ✅. `--independent` ⬜. |
| `status` | ✅ | ✅ | `--verbose` ✅, `--since` (changed-without-changeset guard + narrowing) ✅, `--output` JSON plan ✅, reflects pre-mode like `version` ✅, no-changesets → non-zero exit ✅. (net groups under bump headers — cosmetic diff.) |
| `pre enter`/`exit` | ✅ | ✅ | `.changeset/pre.json` shape, counter, graduation — full parity. |
| `tag` | ✅ | ✅ | `name@version`, skip existing. **🟢 Go module-path tags** (`dir/vX.Y.Z`); **🟢 `--dry-run`**. |
| `publish` | ✅ | ✅ | `--no-git-tag` ✅. **🟢 `--dry-run`/`--no-push`/`--access`**, **🟢 TTY confirm gate + `--yes`** (CI unchanged). Registry-aware idempotent ✅; honors `ignore` ✅. |
| `info` | ✅ | ✅ | Config + ecosystems + packages + changeset count. |
| `ui` | ✅ (Spectre) | ✅ (bubbletea) | Interactive menu dispatching the verbs. |
| `shell-init` | ✅ | ⬜ | net emits a `changeset-net` shell fn. rigsmith has cobra `completion` but not the resolve-the-binary shell function. |
| `release` (orchestrator) | ✅ | ✅ | **Built** (`release/internal/pipeline` + `forge`): see the orchestrator section below. |

## Changeset format & engine

| Feature | net-changesets | rigsmith | Notes |
|---|---|---|---|
| Frontmatter `"Name": bump` | ✅ | ✅ | Multi-bump-per-line read ✅; empty changeset ✅; id ✅. |
| Conventional `type:` + bumpless line | ➖ | 🟢 | rigsmith-only (your feature): `type: feat[!]`, bare `"Name"`, bump derived from type. Not @changesets-readable (fine — no compat). |
| `.net.mkd` interop extension | ✅ | ➖ | Dropped (no Node-interop bridge). |
| Semver bump rules + graduation | ✅ | ✅ | Faithful port, unit-tested (prerelease graduation, precedence). |
| Dependency cascade | ✅ rangeless (always-patch) | 🟢 range-aware | rigsmith does the rangeless case AND npm `^`/`~`/`workspace:` out-of-range gating, `updateInternalDependencies` threshold, **peer→major**, **dev = range-only (no release)**, manifest range rewrites. |
| Grouping: linked / fixed / lockstep | ✅ | ✅ | lockstep via shared `VersionFile` (generalized from `Directory.Build.props`). |
| `ignore` (names + globs) | ✅ | ✅ | |
| `updateInternalDependencies` | ✅ | ✅ | patch/minor threshold honored by the cascade. |
| Version strategy: lockstep | ✅ | ✅ | shared version file moves together. |
| Version strategy: independent + `--independent` | ✅ | ⬜ | No inline-per-project override yet. |
| Prerelease mode (pre.json, counter) | ✅ | ✅ | |
| Snapshot mode (templates, useCalculatedVersion) | ✅ | ✅ | `{tag}`/`{commit}`/`{datetime}`/`{timestamp}`; base 0.0.0 or calculated. |
| Prerelease graduation on exit | ✅ | ✅ | |

## Changelog

| Feature | net-changesets | rigsmith | Notes |
|---|---|---|---|
| Bump-grouped sections + bullets + "Updated dependencies" | ✅ | ✅ | `## version` → `### Major/Minor/Patch Changes`; multi-line indent; insert at line 2. |
| Type-grouped sections (changelogen-style) | ➖ | 🟢 | Driven by `changelogGroups`; built-in + plugins. |
| Default generator | ✅ | ✅ | |
| `changelog-git` (commit hash prefix) | ✅ | ✅ | `core/changelog`: commit-that-added-the-changeset resolved via git log, first line prefixed. |
| `changelog-github` (PR/author via `gh`) | ✅ | ✅ | PR/commit/Thanks links via `gh api`; failures degrade to undecorated lines. The three stock @changesets ids map to the builtin layout (fixed a latent subprocess-resolution bug). |
| Pluggable subprocess generators | ✅ (design only) | 🟢 (implemented) | `ChangelogRequest` contract, `$PATH`/path resolution, built-in dogfoods it, **+ a Node `changelogen` reference plugin**. |
| Formatter `format:` (native/prettier/dprint/…/auto) | ✅ (incl. NativeMarkdownFormatter) | 🟢 | `core/mdfmt`: native formatter port (18 golden tests, idempotent) + dispatch (auto-detect, lockfile-aware pm exec, deno direct, warn-only degradation) **+ 🟢 custom argv escape hatch** (`"format": ["myfmt", "--write"]`). |

## Config (`.changeset/config.json`)

| Key | net-changesets | rigsmith | Notes |
|---|---|---|---|
| `baseBranch` | ✅ | ✅ | |
| `access` | ✅ | ✅ | used by publish `--access` default. |
| `ignore` / `fixed` / `linked` | ✅ | ✅ | |
| `updateInternalDependencies` | ✅ | ✅ | |
| `snapshot.{useCalculatedVersion,prereleaseTemplate}` | ✅ | ✅ | |
| `changelog` | ✅ | ✅ | resolves the generator (default/path/name). |
| `format` | ✅ | 🟢 | full dispatch (false/native/auto/named tool) + the argv custom-command form. |
| `commit` | ✅ (written) | ⬜ | |
| `dotnet.sourcePath` | ✅ | 🟡 | rigsmith uses top-level **`paths`** (🟢) instead of per-ecosystem sourcePath; the `dotnet` block isn't read. |
| `dotnet.packageSource` | ✅ | 🟡 | publish defaults per-ecosystem (`nuget`/npm/crates); not read from a config block yet. |
| `dotnet.versionStrategy` | ✅ | ⬜ | (ties to `--independent`, not built). |
| `dotnet.{interop,changesetExtension,autoRunNode,nodeChangesetCommand}` | ✅ | ➖ | Node-interop bridge — dropped. |
| Per-ecosystem block (generalized) | ➖ | 🟢 | `core/config` keeps a generic `ecosystems` map + `Ecosystem(id, dst)` decoder (not yet wired to read sourcePath/packageSource). |
| `changelogGroups`, `paths` | ➖ | 🟢 | New top-level keys (conventional grouping; discovery narrowing). |
| Legacy flat-key migration | ✅ | ➖ | Not needed (no compat). |

## Release orchestrator (`.changeset/release.jsonc`)

**Built (2026-06-12)** — `release/internal/pipeline` + `release/internal/forge`,
wired as `relrig release`. ✅: `tool` (defaults to relrig itself; set
`"npx changeset"` to drive the Node CLI)/`order`/`steps`(enabled/before/after/
run/args/message/confirm/forge)/`hooks`(before/after/onError)/`vars`(lazy +
eager, cached), CommandSpec (shell string / argv array, mixed lists), `${tool}`/
`${vars.*}`/`${env.*}` interpolation, longest-first secret masking, default
pipeline (version→commit→publish→push→githubRelease), forge auto/github/none
with `gh` probing + CHANGELOG-section release notes, plain + rich (lipgloss)
reporters with resume hints, confirm gates (huh on a TTY; `--yes` otherwise),
`--dry-run/--only/--skip/--from/--to/--config/--yes/--git-only/--ui/--no-ui`,
JSONC config via `core/jsonc`. ⬜ remaining: the interactive step-chooser TUI
(passthrough today) and `packages.versionRegex`.

## Ecosystem / publishing

| Feature | net-changesets | rigsmith | Notes |
|---|---|---|---|
| .NET (.csproj) discover / version-resolve / write | ✅ | ✅ | inline vs Directory.Build.props; format-preserving regex write. |
| Node / Cargo / Go ecosystems | ➖ (Node via interop only) | 🟢 | Native adapters for node, cargo, **go (git-tag versioning)** — beyond net's single .NET path. |
| NuGet publish (pack + push --skip-duplicate, registry-aware) | ✅ | ✅ | |
| npm / cargo publish | ➖ | 🟢 | `npm publish` (idempotent via `npm view`), `cargo publish` (already-detect). |
| Git tagging (`name@version`) | ✅ | ✅ | + Go module-path form. |
| Node interop / autoRunNode delegation | ✅ | ➖ | Dropped (native node adapter replaces it). |

---

# Part B — rig (Go) vs rig (.NET + Node)

`rig` covers the dev loop, package management, and the common maintenance verbs;
the ergonomics tail remains. Below, **.NET** and **Node** columns are the source
tools; **rigsmith** is the Go `cli/` module.

## Verbs

| Verb | .NET | Node | rigsmith | Notes |
|---|---|---|---|---|
| `build` / `test` / `run`/`dev` / `format` | ✅ | ✅ | ✅ | Via each ecosystem's `EcosystemInfo.DevCommands`; `--dry-run`/`--quiet`. |
| `lint` / `typecheck` | ➖ | ✅ | 🟡 | Mapped where the ecosystem declares them (node/cargo); dotnet/go report "no mapping". |
| `coverage` | ✅ | ✅ | ✅ | Native coverage per ecosystem; `--min` gate (go/node/**dotnet**), `--open`; **in-process cobertura→HTML** for .NET (stands in for ReportGenerator); runner auto-MTP via global.json; `.rig.json coverage.*` defaults. |
| `kill` (proc/port) | ✅ | ✅ | ✅ | `--port` (lsof/netstat), name/pattern (pgrep/pkill·taskkill), `kill.match` config, `--dry-run`; short auto-patterns guarded. |
| `add` / `uninstall` / `outdated` | ✅ | ✅ | ✅ | Per-ecosystem native; aliases `remove`/`rm`/`od`. |
| `global` / `dlx` | ✅ | ✅ | ✅ | Per-ecosystem (`dotnet tool install -g`/`dnx`, `go install`, `cargo install`); node pm-aware (`pnpm dlx`, `yarn global add`, `bun x`…). aliases `g`/`x`. |
| `install`/`restore` / `ci` / `upgrade` | ✅ | ✅ | ✅ | Node uses **package-manager detection** (pnpm/yarn/bun → ni-style commands). |
| `clean` | ✅ | ✅ | ✅ | Native per ecosystem; node dist-dir clean ⬜. |
| `rebuild` | ✅ | ✅ | ✅ | Sequences clean → build; alias `rb`. |
| `doctor` | ✅ | ✅ | ✅ | Per-ecosystem env checklist; non-zero exit on errors. |
| `cd` | ✅ | ✅ | ✅ | Tiered fuzzy match (exact/prefix/substring/subsequence, name>path, short-name); prints dir to stdout (needs shell wrapper); picker on TTY; name completion. |
| `publish` (rig's dotnet publish) | ✅ | ➖ | ✅ | rid/output/configuration/self-contained/single-file: flag > `.rig.json publish.*` > default; `{rid}` output templating. |
| `default` / `setup` / `self-update` | ✅ | ✅ | 🟡 | default-project SETTER done (validate + persist via ConfigWriter); the interactive `default` verb, `setup`, `self-update` ⬜. |
| `init` (.rig.json scaffold) | ✅ | ✅ | ✅ | Writes a `.rig.json` with all keys; refuses to overwrite. |
| `completion` | ✅ | ✅ | 🟡 | cobra completion + dynamic name completion (cd); not the self-contained `[suggest]` protocol. |
| scripts → verbs (auto) | ➖ | ✅ | ✅ | Node: every package.json script (not shadowing a built-in) becomes `rig <script>` → `<pm> run <script>` (flags after `--`). |
| custom `commands` | ✅ | ⬜ (gap) | 🟢 | string / argv / object forms with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description`; missing-OS-spec errors cleanly. |
| `watch` modifier | ✅ | ✅ | 🟡 | `rig watch <verb>` / `rig w r` / trailing `--watch` via the pre-parse pipeline; per-verb `--watch` flag at any position ⬜. |
| bare `rig` menu / `ui` | ✅ | ✅ | 🟡 | grouped bubbletea menu (Dependencies ▸ / Maintenance ▸) + breadcrumb/back-nav; project-picker / focus-scoping ⬜. |
| `info` | ✅ | ✅ | ✅ | root, primary ecosystem, `.rig.json`, command mappings, packages (exclude-filtered). |

## Config (`.rig.json`)

| Key | .NET | Node | rigsmith | Notes |
|---|---|---|---|---|
| `defaultProject` | ✅ | ✅ | ✅ | enforced in run/test resolution; settable via the default-setter. |
| `quiet` | ✅ | ✅ | ✅ | |
| `exclude` | ✅ | ✅ | 🟡 | enforced in `info` discovery; not yet in menu pickers. |
| `env` | ✅ | ✅ | ✅ | applied to spawned commands. |
| `kill.match` | ✅ | ✅ | ✅ | patterns for the default kill sweep. |
| `commands` | ✅ | ⬜ | ✅ | |
| `ecosystem` (pin primary) | ➖ | ➖ | 🟢 | new — resolves polyglot ambiguity. |
| `envPresets` / `aliases` / `kill` / `coverage.*` / `dotnet.*` | ✅ | partial | ✅ | full schema parsed (JSONC); `dotnet.*` namespace folds over legacy top-level; `coverage.*` feeds the gate/HTML; unknown keys get did-you-mean warnings. |
| global `~/.rig.json` | ✅ | ✅ | 🟡 | `LoadMerged` (repo-over-global per the C# rules) implemented; not yet wired into Execute. |
| comment-preserving writes | ✅ | ✅ | ✅ | `core/jsonc` editor + ConfigWriter (`$schema` on fresh files, splice on existing, refuse-clobber). |

## Discovery & resolution

| Feature | .NET | Node | rigsmith | Notes |
|---|---|---|---|---|
| Root resolution (walk-up) | ✅ | ✅ | ✅ | |
| Project/package discovery | ✅ | ✅ | ✅ | via `core/ecosystem` adapters (shared with relrig). |
| Nearest-manifest primary + ambiguity stop | ➖ | ➖ | 🟢 | new — polyglot-aware primary selection. |
| Workspace detection (pnpm/yarn/npm/bun) | ➖ | ✅ | ✅ | node adapter resolves workspaces; rig detects the pm (pnpm/yarn/bun) for commands. |
| Monorepo graph / `--all` (topo) / `--filter` | ❌ | ✅ | ✅ | `rig build --all` runs across the workspace in dependency order (topo sort, cycle-tolerant); `--filter <glob>` narrows; works across polyglot packages. |
| Fuzzy match / short-name / verb-prefix / watch-modifier | ✅ | ✅ | ✅ | `rig build <project>` scopes to a package (exact/short-name/substring); `rig cove`→coverage (prefix); `rig watch <verb>`/`rig w r` per ecosystem. (Test-class fuzzy ⬜.) |
| Capabilities probing (hide verbs) | ✅ | ✅ | 🟡 | the menu hides verbs the ecosystem doesn't map; cobra still lists all subcommands. |

## Ecosystem-specific & shell integration

| Feature | .NET | Node | rigsmith | Notes |
|---|---|---|---|---|
| Test enumeration / filter shorthand / MTP-VSTest | ✅ | ➖ | ⬜ | |
| Coverage engine (ReportGenerator / vitest) | ✅ | ✅ | ⬜ | |
| RID/self-contained publish · dnx/dlx · global.json doctor | ✅ | ➖ | ⬜ | |
| ni-parity commands · scripts→verbs · Vite+ · port-kill | ➖ | ✅ | ⬜ | |
| Shell completion (zsh/bash/pwsh) | ✅ | ✅ | 🟡 | cobra default only. |
| `[suggest]` protocol + cross-ecosystem completion | ✅ | ✅ | ⬜ | |
| `rig cd` shell wrapper | ✅ | ✅ | ⬜ | |
| Cross-tool delegation (.NET↔Node) + `rig-net`/`rig-node` | ✅ | ✅ | ➖ | **N/A by design** — one Go binary handles all ecosystems natively; the source split exists only because neither tool could. |
| `RIG_*` env vars | ✅ | ✅ | ⬜ | (most relate to delegation, which is moot here). |

## UX

| Feature | .NET | Node | rigsmith | Notes |
|---|---|---|---|---|
| Interactive menu (groups, pickers, back-nav) | ✅ | ✅ | 🟡 | basic bubbletea menu; no grouped sub-menus / focus scoping yet. |
| `--dry-run` / `--quiet` / `→` echo | ✅ | ✅ | ✅ | |
| `.env` / `.env.local` loading + precedence | ✅ | ✅ | ✅ | `cli/internal/envstack`: exact C# quoting; file < ambient < config < command; wired into every spawn path. |
| env presets as flags | ✅ | ✅ | ⬜ | |
| custom commands (shell/argv/OS/env/cwd) | ✅ | ⬜ | 🟡 | rigsmith runs the shell-string form; no per-OS/env/cwd/argv variants. |
| `--no-env` / `--root` | ✅ | ✅ | ⬜ | |
| self-update | ✅ | ✅ | ⬜ | (GoReleaser/install.sh scaffolded for distribution). |

---

## Suggested next steps (by leverage)

1. **rig ergonomics tail** (Phase 6 core is done): `[suggest]` completion, menu
   project-pickers/focus-scoping, `setup`/`self-update`, the interactive
   `default` verb, test-class fuzzy match, per-verb `--watch` flag, `cd` shell
   wrapper, global `~/.rig.json` wiring (LoadMerged exists), Windows CIM kill.
2. **changerig tail**: `--independent` (+ `dotnet.versionStrategy`), `commit`
   config key, `add --open`, `shell-init`.
3. **relrig tail**: interactive plan-chooser TUI, `packages.versionRegex`,
   NuGet feed-protocol unit tests if a native feed client lands.

*(Done since the original audit: status `--since`/`--output`/pre-mode
reflection, add `--since`, changelog git/github enrichment, the `format:`
formatter incl. the native port, the entire release orchestrator + forge
releases, publish confirm gate + ignore filtering, and the cross-ecosystem
parity corpus with dotnet + polyglot oracles.)*
