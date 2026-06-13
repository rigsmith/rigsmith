# External tools — review

How rig depends on tools it doesn't ship. This is a review/discussion doc, not a
spec: it inventories every external binary rig shells out to, how each is
invoked, whether rig checks for it, what happens when it's missing, and whether
rig can install it — so we can decide where to make the handling more consistent.

## Johns Notes
 - [ ] standardize on .net 10 (but still gracefully fail if .net 8.0 is installed) - if neither is installed, a nice offer to open the .net installation page if the user somehow lands on a dotnet repo


## Two tiers

**Tier 1 — the ecosystem toolchain** (`dotnet`, `go`, `node` + its package
manager, `cargo`). These *are* the ecosystem; rig assumes they're present and
[`rig doctor`](../cli/internal/cli/doctor.go) validates them by version probe
(`go version`, `node --version`, `dotnet --version` incl. global.json pin check,
`cargo --version`). Not covered further here.

**Tier 2 — auxiliary tools** rig invokes *beyond* the core toolchain. These are
the subject of this doc.

## Inventory

| Tool | Used by | Invoked as | Presence check | Auto-install | Config | Missing → |
|---|---|---|---|---|---|---|
| **ReportGenerator** | `rig coverage` (rich HTML; .NET/node/go) | `reportgenerator` · `dotnet tool run reportgenerator` · `dnx -y dotnet-reportgenerator-globaltool` | ✅ `extTool` (LookPath + tool-manifest scan + dnx probe) | ✅ prompt → `dnx`, persists choice | `coverage.reportGenerator` (auto/off/install), `.reportTypes`, `.license` | native fallback report (`ensure`) |
| **dnx** | .NET `dlx`; RG fetch vehicle | `dnx <spec>` | ✅ `extTool` on the `dlx` path (+ LookPath in RG) | — (ships with .NET 10 SDK) | `tools.dnx` | guidance + offer to open dot.net (`require`) |
| **cargo-llvm-cov** | `rig coverage` (cargo) | `cargo llvm-cov` | ✅ `extTool` (LookPath) | ✅ prompt → `cargo install cargo-llvm-cov` | `tools.cargo-llvm-cov` | guidance error (`require`) |
| **cargo-outdated** | `rig outdated` (cargo) | `cargo outdated` | ✅ `extTool` | ✅ prompt → `cargo install cargo-outdated` | `tools.cargo-outdated` | guidance error (`require`) |
| **cargo-watch** | `rig watch` (cargo) | `cargo watch -x <verb>` | ✅ `extTool` | ✅ prompt → `cargo install cargo-watch` | `tools.cargo-watch` | guidance error (`require`) |
| **wgo** | `rig watch` (go) | `wgo go <verb>` | ✅ `extTool` (LookPath) | ✅ prompt → `go install github.com/bokwoon95/wgo@latest` | `tools.wgo` | guidance error (`require`) |
| **vitest** | `rig coverage` (node) | reporters injected into the test run | ✅ config-file/dep detection | — (project dev-dep) | — | reporters not injected (no rich report) |
| **OS process tools** | `rig kill` | `lsof`/`netstat` (port), `pgrep`/`pkill`/`taskkill` (name) | ⚠️ implicit (non-zero exit = "no match") | — | `kill.match` | treated as no matches |
| **git** | release/version/tagging (`core/gitutil`), `rig outdated` discovery | `git …` | ❌ none | — | — | raw error |
| **curl + GitHub API** | `rig self-update`, `install.sh` | `curl … | sh`, `api.github.com/.../releases/latest` | n/a | n/a (this *is* the installer) | n/a |

Legend: ✅ handled · ⚠️ partial/implicit · ❌ none.

## Per-tool detail

### ReportGenerator — the reference implementation

The only tool with the full treatment. [`coverage_report.go`](../cli/internal/cli/coverage_report.go)

- **Resolution order** (`resolveReportGenerator`, [:52](../cli/internal/cli/coverage_report.go#L52)):
  1. `reportgenerator` on `PATH` (a global tool) →
  2. a local `.config/dotnet-tools.json` entry → `dotnet tool run reportgenerator`
     (manifest is searched walking up from root, [:73](../cli/internal/cli/coverage_report.go#L73)) →
  3. mode `download` + `dnx` present → `dnx -y dotnet-reportgenerator-globaltool`
     (fetched on use) →
  4. otherwise unavailable → native fallback.
- **Args** (`buildReportGeneratorArgs`, [:121](../cli/internal/cli/coverage_report.go#L121)):
  `-reports:` (`;`-joined), `-targetdir:`, `-reporttypes:` (default `Html`),
  `-license:` when a Pro key is set.
- **Auto-download** (`resolveReportGeneratorOrPrompt`, [:465](../cli/internal/cli/coverage_report.go#L465)):
  in `auto` mode, on a TTY (not `--quiet`/`--dry-run`), with `dnx` present and RG
  absent, rig prompts **yes / not-now / never**. "Yes" persists
  `coverage.reportGenerator=download` to `.rig.json`; "never" persists `off`;
  "not now" is a one-shot decline. ([persistRGMode](../cli/internal/cli/coverage_report.go#L508))
- **Config**: `coverage.reportGenerator` = `auto`|`off`|`download`,
  `coverage.reportTypes`, `coverage.license`. ([config.go:110](../cli/internal/config/config.go#L110))
- **Fallback**: a dependency-free native report — per-line-highlighted Cobertura
  HTML, Istanbul HTML, or `go tool cover -html`. RG failure also falls back
  ([:184](../cli/internal/cli/coverage_report.go#L184)).

### dnx (.NET 10 tool launcher)

Two roles: the `dlx` verb for .NET ([dotnet.go](../core/ecosystem/dotnet/dotnet.go) `VerbDlx: {"dnx"}`, run via
[newDlxCmd](../cli/internal/cli/dlx.go)) and the fetch vehicle for ReportGenerator.
The `dlx` path now `require`s the `toolDnx` `extTool`, so a box without .NET 10
gets a clear "dnx is required … dnx ships with the .NET 10 SDK" and, on a TTY, an
offer to open the download page — instead of a raw "command not found". rig can't
install dnx itself (it comes with the SDK).

### cargo-llvm-cov / cargo-outdated / cargo-watch

External cargo subcommands invoked as `cargo <sub>`. Each is now an `extTool`
(`toolCargoLlvmCov` / `toolCargoOutdated` / `toolCargoWatch`) that the coverage,
outdated, and watch paths `require`: detected via `exec.LookPath("cargo-<sub>")`,
and when absent, on a TTY rig offers to run `cargo install cargo-<sub>` (persisted
to `tools.<name>`), else fails with that install guidance. The cargo binary stays
the invoker (the `extTool` only gates presence/install).

### wgo (go watch)

Go has no native watcher, so `rig watch` for Go uses
[wgo](https://github.com/bokwoon95/wgo) — which transparently wraps the `go`
command, so the watch argv is just `wgo` prefixed onto the same command the
non-watch verb runs (`wgo go test ./...`, `wgo go run .`, `wgo go vet ./...`).
The watch path `require`s `toolWgo`; when absent, on a TTY rig offers
`go install github.com/bokwoon95/wgo@latest` (persisted to `tools.wgo`), else
fails with that guidance.

### vitest (node coverage)

Not installed by rig — it's a project dev-dependency. rig *detects* it
(`nodeUsesVitest`: config file or `vitest` in deps, [coverage_report.go:517](../cli/internal/cli/coverage_report.go#L517)) and,
when coverage needs machine-readable output, injects
`--coverage --coverage.reporter=lcov --coverage.reporter=html
--coverage.reporter=json-summary` ([:434](../cli/internal/cli/coverage_report.go#L434)). Non-vitest node projects
just don't get the rich report/`--min` gate.

### OS process tools (`rig kill`)

`lsof -ti tcp:<port>` (POSIX) / `netstat -ano` (Windows) for ports; `pgrep`/`pkill`
(POSIX) / `taskkill` (Windows) for names. ([kill.go](../cli/internal/cli/kill.go)) No explicit check —
a non-zero exit is treated as "no matches", so a missing tool reads as "nothing
to kill" rather than erroring.

### git, self-update

`git` is assumed present (used pervasively by `core/gitutil` for tags, version
resolution, and `rig outdated` discovery) with no doctor check. `rig self-update`
and `install.sh` hit `api.github.com/repos/<repo>/releases/latest` and
`curl … | sh`.

## Shared handling: `extTool`

Findings 1–4 below are now implemented by a single abstraction,
[`extTool`](../cli/internal/cli/exttool.go). One value type describes an optional
tool (name, what it's for, how to install it / a hint, plus optional hooks for a
custom resolver, install-ability test, and config key). It centralizes the
lifecycle:

    resolve (present?) → config mode (auto|off|install) → TTY prompt → install → re-resolve

Two entry points share that core:

- **`ensure` → `(invoker, ok)`** for tools *with a fallback*. ReportGenerator
  uses this (its 3-strategy resolver is the `resolve` hook; `coverage.reportGenerator`
  is its config key); `ok=false` means "use the native report".
- **`require` → `(invoker, error)`** for tools with *no fallback*. The cargo
  subcommands and `dnx`-on-`dlx` use this — a missing tool yields a clear
  `… is required (…) — install it with: …` error, so **CI fails cleanly** instead
  of surfacing a cryptic toolchain error.

Modes persist to `.rig.json` under `tools.<name>` (or, for ReportGenerator, its
existing `coverage.reportGenerator` key): "yes" → `install`, "never" → `off`.
`--dry-run` only resolves (never prompts/installs); `require` tolerates a dry-run
miss so the command still prints what it would run. Tools rig can't install
(dnx — ships with the .NET 10 SDK) carry a `hint` and an optional `openURL` that's
offered for opening on a TTY.

## Surfaced in `rig doctor`

`rig doctor` reports a **Tools** group: for the ecosystems present it lists the
relevant `extTool`s (cargo-llvm-cov/outdated/watch, dnx, reportgenerator) as
installed or "not installed — <how to get it>" (a warn — optional tools only
bite when used). The `extTool` values are the source list, so a new tool shows
up automatically. The .NET toolchain check standardizes on .NET 10: an older SDK
warns (the dnx-based features need 10) and a missing SDK offers the dot.net
install page on a TTY.

## Remaining

- **Future tools** drop in as another `extTool` value and are picked up by the
  doctor Tools group automatically (e.g. `rig watch` for Go landed this way via
  wgo).
