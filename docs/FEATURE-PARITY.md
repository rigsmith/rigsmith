# Feature parity audit — rigsmith vs the source tools

Audit of the Go rigsmith implementation against its two source projects
(updated 2026-06-12 after parity phases 1–6; originally 2026-06-11):

- **net-changesets** (.NET) → `changerig` / `relrig` + `core`
- **rig** (.NET + Node, kept at parity) → `rig` (the `cli/` module) + `core`

> Companion docs: [test-parity.md](../test-parity.md) tracks *test* coverage
> per C# suite; this file tracks the *feature* surface. Behavior is pinned by
> the parity corpus (`core/testdata/parity/`, 22 scenarios, Node + C# oracles).

**Source columns** (net-changesets / .NET / Node): ✅ has it · ➖ n/a, out of
scope for that tool · ❌ lacks it.

**rigsmith column:** ✅ = done — identical to the source, or a deliberate
difference that's fully resolved (final, nothing pending); ⬜ = a difference is
still pending resolution, or the feature isn't built.

**Diffs column** (resolved differences only): 🟢 = rigsmith does **more** than
the source — an extra capability, a native replacement, or a rigsmith-only
feature; 🟡 = rigsmith does **less** — a source capability that's reduced or
absent, but accepted (won't fix). A still-*pending* gap is ⬜, not 🟡. Blank =
exact parity.

**Diff Details column:** when Diffs shows a dot, exactly what differs and why;
blank otherwise.

**Notes column:** everything else — what's implemented and how.

**Next steps column:** the concrete work to close a pending (⬜) gap; blank when
rigsmith is done.

> **No .NET back-compat** ([[rigsmith-no-dotnet-compat]]): items noted as "dropped"
> are deliberate — rigsmith does not preserve net-changesets' Node-interop bridge,
> its `dotnet`-block config fidelity, or `.changeset/*.md` dual-tool readability.
> Those show as ✅ rigsmith + 🟡 diff (accepted reduction).

## Headline

- **Release engine (changerig/relrig): at or above parity.** The full
  `init → add → status → version → publish → tag → release` loop works across
  **four** ecosystems (net-changesets had one), with a **range-aware cascade**,
  an **implemented pluggable-changelog system** that net-changesets only
  designed, the **`release` orchestrator** (steps/hooks/vars/gates/forge),
  **changelog git/github enrichment**, and the **markdown formatter**
  (`format:` incl. the native prettier-equivalent and a custom-command
  escape hatch). The changerig config surface is now complete: `--independent`,
  the `commit` key (auto-commit on version/add), and the **generalized
  per-ecosystem config block** (`sourcePath`/`packageSource`/`versionStrategy`,
  consumed by discovery/publish/planner) all land here. The relrig interactive
  step-chooser is now built too — a bubbletea **plan editor** (toggle steps
  pre-run) plus a **live run dashboard** (streaming status + inline confirm
  gates) — and `packages.versionRegex` landed as a generic `regex` ecosystem
  adapter, so the release surface is complete.
- **`rig` (dev launcher): high (Phase 6, 2026-06-12).** Dev loop + full package
  management + `coverage` (incl. .NET `--min` gate + in-process cobertura HTML)
  + `kill` (C#-aligned semantics) + `doctor`/`cd`/`init`/`rebuild`/`publish`/
  `global`/`dlx` + node scripts→verbs + **`--all` topo graph + `--filter` +
  project scoping + verb-prefix + watch-modifier pipeline** + capability-probed
  menu + **JSONC `.rig.json`** (merge, namespaces, rich per-OS commands,
  did-you-mean warnings, comment-preserving writes) + **`.env`/`.env.local`
  layering** + C#-precedence root resolution + full .NET project discovery
  (slnx/sln). The ergonomics tail landed
  2026-06-12: `default`/`setup` (shell integration)/`self-update`, dynamic
  completion + the `cd` shell wrapper, menu focus scoping, per-verb `--watch`,
  test-class fuzzy, Windows CIM kill, global `~/.rig.json`. One architectural win:
  **no cross-tool delegation needed** — the single Go binary handles every
  ecosystem natively (the .NET/Node rig split exists only because neither
  could).

---

# Part A — changerig / relrig vs net-changesets

## Commands

| Feature | net-changesets | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|
| `init` | ✅ | ✅ | 🟡 | Simpler by design: already-initialized is a benign no-op (exit 0) rather than net's distinct `AlreadyInitialized` code, and there's no interactive walkthrough — the prompts it would show (sourcePath/packageSource/interop) don't apply, since rigsmith uses top-level `paths` and dropped the Node-interop bridge. | Creates `.changeset/` + `config.json` + README. | |
| `add` (default) | ✅ | ✅ | 🟢 | Adds `--type`/`-t` (conventional) + `--bump` + omittable bump beyond net. | `-m/--message`, `-p/--package`, `--empty`, `--since` (picker preselect), `--open` ($EDITOR on the created changeset), human-id filename. | |
| `version` | ✅ | ✅ | 🟡 | snapshot-template flag is named `--snapshot-template` (net: `--snapshot-prerelease-template`). | normal/snapshot/pre/exit modes; `--snapshot[=tag]` + template; `--independent` (inline per-package versioning; also a `versionStrategy` config key); changelog enrichment + `format:` pass. | |
| `status` | ✅ | ✅ | | | `--verbose`, `--since` (changed-without-changeset guard + narrowing), `--output` JSON plan, pre-mode reflection, no-changesets → non-zero exit. (net groups under bump headers — cosmetic.) | |
| `pre enter`/`exit` | ✅ | ✅ | | | `.changeset/pre.json` shape, counter, graduation — full parity. | |
| `tag` | ✅ | ✅ | 🟢 | Adds Go module-path tags (`dir/vX.Y.Z`) and `--dry-run` beyond net. | `name@version`, skip existing. | |
| `publish` | ✅ | ✅ | 🟢 | Adds `--dry-run`/`--no-push`/`--access` and a TTY confirm gate + `--yes` (CI behavior unchanged). | `--no-git-tag`; registry-aware idempotent; honors `ignore`. | |
| `info` | ✅ | ✅ | | | Config + ecosystems + packages + changeset count. | |
| `ui` | ✅ (Spectre) | ✅ (bubbletea) | | | Interactive menu dispatching the verbs — different toolkit, same surface. | |
| `shell-init` | ✅ | ✅ | 🟢 | Obviated — net's shell fn resolved the .NET/Node tool split; rigsmith is one binary on PATH (and aliases `changeset`), so no resolve-the-binary wrapper is needed. | cobra `completion` covers tab-completion. | |
| `release` (orchestrator) | ✅ | ✅ | 🟢 | Adds the live bubbletea run dashboard (streaming per-step status + inline confirm gates) on top of the source's interactive step picker, plus forge releases + 4-ecosystem reach. | Built (`release/internal/pipeline` + `forge`) — see the orchestrator section. Interactive flow: a **plan editor** (toggle which steps run) then a **live dashboard**; `packages.versionRegex` shipped as the generic `regex` ecosystem adapter. | |

## Changeset format & engine

| Feature | net-changesets | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|
| Frontmatter `"Name": bump` | ✅ | ✅ | | | Multi-bump-per-line read; empty changeset; id. | |
| Conventional `type:` + bumpless line | ➖ | ✅ | 🟢 | rigsmith-only feature — net has no equivalent; `type: feat[!]` + bare `"Name"` with bump derived from type. Not @changesets-readable (no compat needed). | | |
| `.net.mkd` interop extension | ✅ | ✅ | 🟡 | Dropped — rigsmith changesets aren't @changesets/`.net.mkd` dual-readable (no Node-interop bridge; deliberate). | | |
| Semver bump rules + graduation | ✅ | ✅ | | | Faithful port, unit-tested (prerelease graduation, precedence). | |
| Dependency cascade | ✅ rangeless (always-patch) | ✅ | 🟢 | net is rangeless (always-patch); rigsmith adds range-aware gating — npm `^`/`~`/`workspace:` out-of-range, peer→major, dev = range-only (no release), manifest range rewrites. | Rangeless case + `updateInternalDependencies` threshold both honored. | |
| Grouping: linked / fixed / lockstep | ✅ | ✅ | | | lockstep via shared `VersionFile` (generalized from `Directory.Build.props`). | |
| `ignore` (names + globs) | ✅ | ✅ | | | | |
| `updateInternalDependencies` | ✅ | ✅ | | | patch/minor threshold honored by the cascade. | |
| Version strategy: lockstep | ✅ | ✅ | | | shared version file moves together. | |
| Version strategy: independent + `--independent` | ✅ | ✅ | | | Inline per-project via `--independent`, the top-level `versionStrategy` key, and a per-ecosystem `versionStrategy` override (resolved per package in the planner). | |
| Prerelease mode (pre.json, counter) | ✅ | ✅ | | | | |
| Snapshot mode (templates, useCalculatedVersion) | ✅ | ✅ | | | `{tag}`/`{commit}`/`{datetime}`/`{timestamp}`; base 0.0.0 or calculated. | |
| Prerelease graduation on exit | ✅ | ✅ | | | | |

## Changelog

| Feature | net-changesets | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|
| Bump-grouped sections + bullets + "Updated dependencies" | ✅ | ✅ | | | `## version` → `### Major/Minor/Patch Changes`; multi-line indent; insert at line 2. | |
| Type-grouped sections (changelogen-style) | ➖ | ✅ | 🟢 | rigsmith-only — net has no type-grouped output; driven by `changelogGroups` (built-in + plugins). | | |
| Default generator | ✅ | ✅ | | | | |
| `changelog-git` (commit hash prefix) | ✅ | ✅ | | | `core/changelog`: commit-that-added-the-changeset resolved via git log, first line prefixed. | |
| `changelog-github` (PR/author via `gh`) | ✅ | ✅ | | | PR/commit/Thanks links via `gh api`; failures degrade to undecorated lines. The three stock @changesets ids map to the builtin layout. | |
| Pluggable subprocess generators | ✅ (design only) | ✅ | 🟢 | net only *designed* this; rigsmith *implemented* it. | `ChangelogRequest` contract, `$PATH`/path resolution, built-in dogfoods it, + a Node `changelogen` reference plugin. | |
| Formatter `format:` (native/prettier/dprint/…/auto) | ✅ (incl. NativeMarkdownFormatter) | ✅ | 🟢 | Adds a custom argv escape hatch (`"format": ["myfmt","--write"]`) beyond net. | `core/mdfmt`: native formatter port (18 golden tests, idempotent) + dispatch (auto-detect, lockfile-aware pm exec, deno direct, warn-only degradation). | |

## Config (`.changeset/config.json`)

| Key | net-changesets | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|
| `baseBranch` | ✅ | ✅ | | | | |
| `access` | ✅ | ✅ | | | used by publish `--access` default. | |
| `ignore` / `fixed` / `linked` | ✅ | ✅ | | | | |
| `updateInternalDependencies` | ✅ | ✅ | | | | |
| `snapshot.{useCalculatedVersion,prereleaseTemplate}` | ✅ | ✅ | | | | |
| `changelog` | ✅ | ✅ | | | resolves the generator (default/path/name). | |
| `format` | ✅ | ✅ | 🟢 | Adds the argv custom-command form beyond net. | full dispatch (false/native/auto/named tool). | |
| `commit` | ✅ (written) | ✅ | | | Read via the polymorphic `commit` value (false/true/`[resolver,…]`): `version` auto-commits the bumps + changelogs + changeset deletions as "Version Packages"; `add` commits just the new changeset (its summary as the message). Snapshot runs opt out (throwaway). | |
| `dotnet.sourcePath` | ✅ | ✅ | | | Read from the per-ecosystem block's `sourcePath`, narrowing discovery for that ecosystem only; the top-level `paths` key is the default. | |
| `dotnet.packageSource` | ✅ | ✅ | | | Read from the per-ecosystem `packageSource` block (publish feed/registry); falls back to the built-in default (`nuget` for .NET, adapter default otherwise). | |
| `dotnet.versionStrategy` | ✅ | ✅ | | | Per-ecosystem `versionStrategy` overrides the top-level for that ecosystem's packages; the planner resolves the strategy per package. `version --independent` still forces all. | |
| `dotnet.{interop,changesetExtension,autoRunNode,nodeChangesetCommand}` | ✅ | ✅ | 🟡 | Interop config block dropped — no Node-interop bridge or `.net.mkd` extension to configure (deliberate). | | |
| Per-ecosystem block (generalized) | ➖ | ✅ | 🟢 | Generalized beyond net's single `dotnet` block: a typed `EcosystemConfig` (`sourcePath`/`packageSource`/`versionStrategy`), keyed by adapter id (dotnet/node/go/cargo) and read via `EcoConfig(id)`, is consumed by discovery, publish, and the planner. The replacement for the `dotnet.*` block. | | |
| `changelogGroups`, `paths` | ➖ | ✅ | 🟢 | rigsmith-only top-level keys (conventional grouping; discovery narrowing) — and they *are* consumed (unlike the per-ecosystem block). | | |
| Legacy flat-key migration | ✅ | ✅ | 🟡 | Not ported — rigsmith won't auto-migrate net's legacy flat config keys (no compat). | | |

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
JSONC config via `core/jsonc`. `packages.versionRegex` is implemented as the
generic `regex` ecosystem adapter (`core/ecosystem/regex`): declare files +
named-capture patterns in a `.changeset/config.json` `regex` block and they
version like any other package (tag-only release). The interactive flow is built
(`release/internal/cli/planeditor.go` + `dashboard.go`): on an interactive rich
TTY, a bubbletea **plan editor** lets you toggle which steps run, then a **live
dashboard** drives the run (per-step status, streamed output, inline confirm
gates) — one program owns the terminal via a `Reporter`→`tea.Msg` bridge and a
confirm round-trip. CI / `--yes` / piped / `--no-ui` / `--dry-run` keep the
sequential plain/rich reporters. See [UI.md](UI.md).

## Ecosystem / publishing

| Feature | net-changesets | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|
| .NET (.csproj) discover / version-resolve / write | ✅ | ✅ | | | inline vs Directory.Build.props; format-preserving regex write. | |
| Node / Cargo / Go ecosystems | ➖ (Node via interop only) | ✅ | 🟢 | rigsmith-only — net handled only .NET (Node via interop); rigsmith adds native node, cargo, go (git-tag versioning) adapters. | | |
| `regex` adapter (arbitrary files, `versionRegex`) | ✅ (`packages.versionRegex`) | ✅ | 🟢 | net's per-package `versionRegex` lives here as a first-class `regex` ecosystem adapter (`core/ecosystem/regex`): a `.changeset/config.json` `regex` block names files + named-capture patterns (`(?<version>…)`, also accepts Go's `(?P<version>…)`); discover/SetVersion go through the normal plugin contract, released tag-only (`name@version`). | | |
| NuGet publish (pack + push --skip-duplicate, registry-aware) | ✅ | ✅ | | | | |
| npm / cargo publish | ➖ | ✅ | 🟢 | rigsmith-only — net had no npm/cargo publish. | `npm publish` (idempotent via `npm view`), `cargo publish` (already-detect). | |
| Git tagging (`name@version`) | ✅ | ✅ | 🟢 | Adds the Go module-path tag form beyond net's `name@version`. | | |
| Node interop / autoRunNode delegation | ✅ | ✅ | 🟢 | Replaced by the native node adapter — no JS-interop delegation needed (more self-contained). | | |

---

# Part B — rig (Go) vs rig (.NET + Node)

`rig` covers the dev loop, package management, and the common maintenance verbs;
a small ergonomics tail remains. Below, **.NET** and **Node** columns are the
source tools; **rigsmith** is the Go `cli/` module.

## Verbs

| Verb | .NET | Node | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|---|
| `build` / `test` / `run`/`dev` / `format` | ✅ | ✅ | ✅ | 🟢 | Via each ecosystem's `EcosystemInfo.DevCommands`; `--dry-run`/`--quiet`. `format` on .NET also supports **CSharpier** as an alternative to `dotnet format` — selected by `dotnet.formatter` (auto/dotnet/csharpier), `auto` opting in on a `.csharpierrc`/tool-manifest; offered for install via the extTool path. | |
| `lint` / `typecheck` | ➖ | ✅ | ✅ | 🟡 | node→`run lint`/`run typecheck`; cargo→`clippy`/`cargo check`; go→`go vet ./...` for both (Go folds type-checking into vet — no separate pass). Only .NET has no standard lint/typecheck verb, so it reports "no mapping". | | dotnet lint/typecheck (no native SDK verb). |
| `coverage` | ✅ | ✅ | ✅ | 🟢 | Uses **ReportGenerator** when present (cross-platform; .NET via Cobertura, node via lcov, go via an in-process profile→Cobertura conversion) — beyond the C#'s .NET-only RG; native fallback otherwise (per-line-highlighted Cobertura HTML, Istanbul HTML, `go tool cover -html`). Configurable: `coverage.reportGenerator` = auto/off/download. cargo→`cargo llvm-cov` (defers to the tool's own per-file/TOTAL table; `--min`→`--fail-under-lines`, `--open`→`--html`; rig's summary table/`--browse` not wired for cargo yet). | `--min` gate (go/node/dotnet/cargo), `--open`, vitest reporter auto-injection, runner auto-MTP via global.json, `.rig.json coverage.*` defaults. On a TTY with RG absent, offers to download it (yes / not-now / never, remembered in `.rig.json`). | |
| `kill` (proc/port) | ✅ | ✅ | ✅ | | | `--port` (lsof/netstat), name/pattern (pgrep/pkill·taskkill), `kill.match` config, `--dry-run`; short auto-patterns guarded. | |
| `add` / `uninstall` / `outdated` | ✅ | ✅ | ✅ | | | Per-ecosystem native; aliases `remove`/`rm`/`od`. `outdated`: dotnet/node/go native, cargo via the `cargo-outdated` subcommand. `uninstall`: native for dotnet/node/cargo; Go has no removal verb, so it's `go get pkg@none` + `go mod tidy` (a bare `rig uninstall` just tidies). | |
| `global` / `dlx` | ✅ | ✅ | ✅ | | | Per-ecosystem (`dotnet tool install -g`/`dnx`, `go install`, `cargo install`); node pm-aware (`pnpm dlx`, `yarn global add`, `bun x`…). `dlx`: .NET→`dnx`, node→npx/bun x/dlx, go→`go run pkg@latest`; Cargo has no one-shot run. aliases `g`/`x`. | |
| `install`/`restore` / `ci` / `upgrade` | ✅ | ✅ | ✅ | 🟢 | Node uses package-manager detection (pnpm/yarn/bun → ni-style commands). `ci` (frozen install) per ecosystem: npm ci / immutable·frozen-lockfile (pm-aware), `dotnet restore --locked-mode`, `cargo fetch --locked`, `go mod download` (Go has no distinct frozen mode — checksums verified against go.sum). `upgrade`: a bare `rig upgrade` is range-respecting — for ecosystems with a parseable report (go/node[npm·pnpm·bun·yarn-classic]/cargo/.NET) it shows the in-range plan and, on a TTY unless `--yes`, asks one confirm before upgrading (node/cargo run their native bulk command; go/.NET pin per-package; cargo's plan comes from `cargo update --dry-run`). .NET, which has no ranges, goes to latest — the first-class .NET upgrade verb the SDK lacks. `rig upgrade <pkg>` targets named packages; `rig outdated -i` is the to-latest selective picker. | | |
| `clean` | ✅ | ✅ | ✅ | | | Native per ecosystem (`dotnet`/`go`/`cargo clean`); Node has no canonical clean, so it maps to the project's `clean` script (same convention as build/test/format) — `rebuild` skips it when the project defines none. | |
| `rebuild` | ✅ | ✅ | ✅ | | | Sequences clean → build; alias `rb`. | |
| `doctor` | ✅ | ✅ | ✅ | 🟢 | Per-ecosystem env checklist; non-zero exit on errors. Adds a **Tools** group (optional `extTool`s — cargo-llvm-cov/outdated/watch, dnx, reportgenerator — present/absent for the detected ecosystems) and a **Config** group (validates `.rig.json` paths resolve: `solution` file, `defaultProject` names a real project, custom-command `cwd`). The .NET check standardizes on .NET 10 — older SDKs warn (dnx features need 10), a missing SDK offers the install page on a TTY. | |
| `cd` | ✅ | ✅ | ✅ | | | Tiered fuzzy match (exact/prefix/substring/subsequence, name>path, short-name); prints dir to stdout (needs shell wrapper); picker on TTY; name completion. | |
| `publish` (rig's dotnet publish) | ✅ | ➖ | ✅ | | | rid/output/configuration/self-contained/single-file: flag > `.rig.json publish.*` > default; `{rid}` output templating. | |
| `default` / `setup` / `self-update` | ✅ | ✅ | ✅ | 🟢 | `setup` diverges and goes further: the C# verb was an interactive config walkthrough (covered in Go by `init`+`default`); Go's `setup` installs the full shell integration (cd wrapper + completion) into zsh/bash/fish rc files, idempotently. | `default`: print/picker/persist (comment-preserving). `self-update`: GitHub-releases check vs the ldflags-stamped version, install.sh handoff. | |
| `init` (.rig.json scaffold) | ✅ | ✅ | ✅ | | | Writes a `.rig.json` with all keys; refuses to overwrite. | |
| `completion` | ✅ | ✅ | ✅ | 🟢 | rigsmith uses cobra's `__complete`; the bespoke `[suggest]` protocol isn't needed — dynamic completions cover the same surface. | cobra completion + dynamic project/runnable completion (cd/run/test/build/kill/publish/default); installed by `rig setup`. | |
| scripts → verbs (auto) | ➖ | ✅ | ✅ | | | Node: every package.json script (not shadowing a built-in) becomes `rig <script>` → `<pm> run <script>` (flags after `--`). | |
| custom `commands` | ✅ | ⬜ (gap) | ✅ | 🟢 | Adds the argv form over the Node rig's gap (Node lacked custom commands). | string / argv / object forms with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description`; missing-OS-spec errors cleanly. | |
| `watch` modifier | ✅ | ✅ | ✅ | | | `rig watch <verb>` / `rig w r` / and a real position-independent `--watch`/`-w` on run/build/test. | |
| bare `rig` menu / `ui` | ✅ | ✅ | ✅ | 🟢 | Adds project picker / focus scoping (`rig · <project>` crumb; dev verbs + kill run scoped) beyond both sources. | grouped bubbletea menu + breadcrumb/back-nav. | |
| `info` | ✅ | ✅ | ✅ | | | root, primary ecosystem, `.rig.json`, command mappings, packages (exclude-filtered). | |

## Config (`.rig.json`)

| Key | .NET | Node | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|---|
| `defaultProject` | ✅ | ✅ | ✅ | | | enforced in run/test resolution; settable via the default-setter. | |
| `quiet` | ✅ | ✅ | ✅ | | | | |
| `exclude` | ✅ | ✅ | ✅ | | | enforced in `info` discovery, the cross-ecosystem pickers (menu focus, `cd`, dev-verb scoping/`--all`/completion, `watch`), and the kill sweep — matched by full or short package name. | |
| `env` | ✅ | ✅ | ✅ | | | applied to spawned commands. | |
| `kill.match` | ✅ | ✅ | ✅ | | | patterns for the default kill sweep. | |
| `commands` | ✅ | ⬜ | ✅ | | | full support incl. per-OS/env/cwd. | |
| `ecosystem` (pin primary) | ➖ | ➖ | ✅ | 🟢 | rigsmith-only — neither source had it; resolves polyglot ambiguity. | | |
| `envPresets` / `aliases` / `kill` / `coverage.*` / `dotnet.*` | ✅ | partial | ✅ | | | full schema parsed (JSONC); `dotnet.*` namespace folds over legacy top-level; `coverage.*` feeds the gate/HTML; unknown keys get did-you-mean warnings. | |
| global `~/.rig.json` | ✅ | ✅ | ✅ | | | wired everywhere (commandEnv, custom commands, info, ui, kill, coverage, publish); `$RIG_GLOBAL_CONFIG` test seam; self-merge guard when cwd is $HOME. | |
| comment-preserving writes | ✅ | ✅ | ✅ | | | `core/jsonc` editor + ConfigWriter (`$schema` on fresh files, splice on existing, refuse-clobber). | |

## Discovery & resolution

| Feature | .NET | Node | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|---|
| Root resolution (walk-up) | ✅ | ✅ | ✅ | | | | |
| Project/package discovery | ✅ | ✅ | ✅ | | | via `core/ecosystem` adapters (shared with relrig). | |
| Nearest-manifest primary + ambiguity stop | ➖ | ➖ | ✅ | 🟢 | rigsmith-only — neither source had it; polyglot-aware primary selection. | | |
| Workspace detection (pnpm/yarn/npm/bun) | ➖ | ✅ | ✅ | | | node adapter resolves workspaces; rig detects the pm (pnpm/yarn/bun) for commands. | |
| Monorepo graph / `--all` (topo) / `--filter` | ❌ | ✅ | ✅ | | | `rig build --all` runs across the workspace in dependency order (topo sort, cycle-tolerant); `--filter <glob>` narrows; works across polyglot packages. | |
| Fuzzy match / short-name / verb-prefix / watch-modifier | ✅ | ✅ | ✅ | | | `rig build <project>` scopes to a package (exact/short-name/substring); `rig cove`→coverage (prefix); `rig watch <verb>`/`rig w r` per ecosystem. Test-class fuzzy: `rig test MyClass` tiered-matches classes scanned from test sources (see Test enumeration row) and builds the C# `--filter` shapes. | |
| Capabilities probing (hide verbs) | ✅ | ✅ | ✅ | | | menu gating + dynamic project-name completion on run/test/build/kill/publish/default/cd (cobra still lists subcommands in help — cosmetic). | |

## Ecosystem-specific & shell integration

| Feature | .NET | Node | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|---|
| Test enumeration / filter shorthand / MTP-VSTest | ✅ | ➖ | ✅ | 🟢 | Enumerates via the platform's own discovery (`dotnet test --list-tests`) instead of the C#'s MetadataLoadContext reflection: VSTest emits fully-qualified names (accurate, framework-agnostic); MTP runners that list only display names (e.g. MSTest) fall back to the source scan. Both shapes are pinned by the `testdata/dotnet` vstest + mtp fixtures. | filter shorthands + MTP/VSTest arg forms + class fuzzy; discovery shows a spinner while dotnet builds + lists. | |
| Coverage engine (ReportGenerator / vitest) | ✅ | ✅ | ✅ | 🟢 | ReportGenerator is used for **all** ecosystems where it can read the output (.NET Cobertura, node lcov, go via conversion) — the C# wired RG for .NET only; vitest reporters are auto-injected so lcov/html/json-summary exist. Native fallback when RG is absent. Detection + mode configurable (`coverage.reportGenerator`, `coverage.license`). | See the `coverage` verb. | |
| RID/self-contained publish · dnx/dlx · global.json doctor | ✅ | ➖ | ✅ | | | Done — `publish` (rid/`--self-contained`/`PublishSingleFile`), the `dnx`/`dlx` verbs, and `doctor` + test-runner detection both read `global.json`. | |
| ni-parity commands · scripts→verbs · Vite+ · port-kill | ➖ | ✅ | ✅ | | | `dev` runs the package.json script (which launches Vite); Vite is detected (config file / `vite` dep) so a bare `rig kill` also frees the dev-server port (`--port` from the dev script, a `port:` in the vite config, else 5173). Plus pm-detected `install`/`ci`/`upgrade`, scripts→verbs, `kill --port`. | |
| Shell completion (zsh/bash/pwsh) | ✅ | ✅ | ✅ | | | cobra completion + dynamic project/runnable completion; `setup` installs the sourcing (zsh/bash/fish; pwsh prints only, like the C#). | |
| `[suggest]` protocol + cross-ecosystem completion | ✅ | ✅ | ✅ | 🟢 | cobra owns `__complete`; dynamic completions cover the same surface, so the bespoke argv protocol isn't needed. | | |
| `rig cd` shell wrapper | ✅ | ✅ | ✅ | | | `rig setup` installs a `rig()` function that cds on `rig cd` and passes everything else through (zsh/bash/fish; verified live). | |
| Cross-tool delegation (.NET↔Node) + `rig-net`/`rig-node` | ✅ | ✅ | ✅ | 🟢 | Obviated — one Go binary handles all ecosystems natively; the source split existed only because neither tool could. | | |
| `RIG_*` env vars | ✅ | ✅ | ✅ | 🟡 | Intentionally absent — most relate to delegation, which is moot in a single binary. | | |

## UX

| Feature | .NET | Node | rigsmith | Diffs | Diff Details | Notes | Next steps |
|---|---|---|---|---|---|---|---|
| Interactive menu (groups, pickers, back-nav) | ✅ | ✅ | ✅ | 🟢 | Adds focus scoping beyond both sources. | groups, project picker, back-nav. | |
| `--dry-run` / `--quiet` / `→` echo | ✅ | ✅ | ✅ | | | | |
| `.env` / `.env.local` loading + precedence | ✅ | ✅ | ✅ | | | `cli/internal/envstack`: exact C# quoting; file < ambient < config < command; wired into every spawn path. | |
| env presets as flags | ✅ | ✅ | ✅ | | | Each `.rig.json` env preset becomes a `--<name>` flag on the dev verbs (`rig test --log`); selected presets merge as the top env layer. Names colliding with a reserved flag are skipped. | |
| custom commands (shell/argv/OS/env/cwd) | ✅ | ⬜ | ✅ | 🟢 | Adds the argv form over the Node rig's gap. | Full support — shell/argv specs, per-OS (`macos`/`windows`/`linux`) overrides via `Resolve()`, plus per-command `env` and `cwd` (`cli/internal/config/command.go`, executed in `scripts.go`). | |
| `--no-env` / `--root` | ✅ | ✅ | ✅ | | | Persistent root flags: `--no-env` drops the `.env`/`.env.local` layer for the run; `--root <dir>` overrides walk-up discovery (every verb resolves through `resolveRoot`). | |
| self-update | ✅ | ✅ | ✅ | | | `rig self-update` (+ menu entry): releases/latest vs stamped version, install.sh handoff; goreleaser now stamps the version ldflag. | |

---

## Suggested next steps (by leverage)

1. **rig leftovers**: the C#-style interactive config walkthrough if wanted
   (Go's `setup` became the shell installer instead) and a relrig version seam
   for its own self-update. The ergonomics tail otherwise landed — env presets
   as flags, `--no-env`/`--root`, and node `clean` (project `clean` script) are
   now done.
2. **relrig tail**: NuGet feed-protocol unit tests if a native feed client
   lands. (The interactive step-chooser TUI — plan editor + live dashboard — and
   `packages.versionRegex` are now built.)

*(Done since the original audit: status `--since`/`--output`/pre-mode
reflection, add `--since`, changelog git/github enrichment, the `format:`
formatter incl. the native port, the entire release orchestrator + forge
releases, publish confirm gate + ignore filtering, the cross-ecosystem
parity corpus with dotnet + polyglot oracles, and — most recently — the full
changerig config surface: the `commit` key, the generalized per-ecosystem
config block (`sourcePath`/`packageSource`/`versionStrategy`), `exclude`
honored across rig's pickers, the `regex` versionRegex adapter, and the relrig
interactive release TUI — plan editor + live dashboard.)*
