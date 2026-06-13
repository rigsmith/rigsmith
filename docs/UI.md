# Interactive UI surface — rigsmith

What interactive / terminal UI exists today across the three tools (`rig`,
`changerig`, `relrig`), when each appears and why, what the user sees, and what
it does. Companion to [FEATURE-PARITY.md](FEATURE-PARITY.md) (feature surface)
— this file is the UI inventory. A gap analysis vs the .NET/Node sources lives
at the end.

## Toolkit & conventions

All TUI is built on the Charmbracelet stack:

- **bubbletea** — the full-screen-ish interactive menus (`rig ui`, `changerig ui`).
- **huh** — one-shot prompts: forms (`changerig add`), single-selects (`rig cd`,
  `rig default`, coverage download), confirms (`publish`, `release` gates).
- **lipgloss** — all coloring/borders/boxes (menus, reporters, plan tables,
  spinners). Used for *styled-but-not-interactive* output too.
- **charmbracelet/x/term** / `golang.org/x/term` — TTY detection.

**TTY philosophy.** Interactive UI only appears on a real terminal. Off a TTY
(CI, pipes, redirected output) every surface degrades to a deterministic
non-interactive path — it never hangs waiting for input. Two helpers gate this:

- `interactive()` (`cd.go`) → `term.IsTerminal(stdin) && term.IsTerminal(stderr)`
  — used by the pickers that draw on stderr (`cd`, `default`, coverage prompt).
- `writerIsTerminal(w)` (`listtests.go`) → true only when the writer is a real
  `*os.File` TTY — lets the spinner fall back cleanly in tests/pipes.

**Bypass flags.** `--dry-run` skips side-effects (and the prompts guarding them);
`--quiet` suppresses the `→ command` echo and the spinner; `--yes`/`-y` bypasses
confirm gates (the CI path).

**Escape cancels.** Every one-shot huh picker (`cd`/`default`/coverage/workspace/
kill) binds **esc** (and ctrl+c) to cancel — backing out is always the no-op
path. The bubbletea models (menu, plan editor, dashboards) handle esc/q too.

**Color legend (lipgloss).** dim gray `245` (secondary text), cyan `14`
(cursor/selection/spinner), magenta `13` (menu title), and the bump palette —
major = red `9`, minor = yellow `11`, patch = green `10`.

---

# `rig` (dev launcher, `cli/`)

## `rig` / `rig ui` — the main menu (bubbletea)
- **Trigger:** `rig ui`, or bare `rig` with no verb. First resolves the repo
  root + primary ecosystem; errors out clearly if the ecosystem is ambiguous.
- **Why:** a discoverable, capability-aware launcher for the everyday verbs.
- **What you see:** a header (`<root>  ·  <primary ecosystem>`), then a grouped
  list. Top level holds the dev verbs (build/test/run/format/lint/typecheck);
  a `▸ Dependencies` submenu (install/ci/outdated/upgrade); a `▸ Maintenance`
  submenu (clean, coverage, kill, doctor, self-update). The cursor is a cyan
  `▸`; the selected label is bold cyan; descriptions are dim. A breadcrumb shows
  the path (`rig`, `rig · <project>`, `rig / Maintenance`). Hint line:
  `↑/↓ move · enter select · esc back · q quit`.
- **Capability probing:** only verbs the primary ecosystem actually maps are
  shown. For .NET it probes the repo (no test project → no test/coverage; no
  runnable project → no run). `kill`/`doctor` always appear.
- **Keys:** `↑/↓`/`k`/`j` move, `enter`/`l`/`→` select (open submenu / pick
  focus / run), `esc`/`backspace`/`h`/`←` back, `q`/`ctrl+c` quit.
- **What it does:** selecting a verb quits the menu and dispatches it (routing to
  the standalone command — coverage/doctor/kill/self-update — or the generic
  ecosystem verb), scoped to the focused project if one is set.
- **Non-TTY:** assumes a terminal; non-interactive invocation fails fast rather
  than hanging.

## Project focus picker (inside the menu)
- **Trigger:** appears as a top entry **only when more than one project is
  discovered**; selecting it opens a sub-frame.
- **What you see:** `(whole repo)` (clears focus) followed by every project name.
  It's the menu's own frame — same arrow/enter navigation, not a separate widget.
- **What it does:** sets the focus; the breadcrumb becomes `rig · <project>` and
  subsequent dev verbs + `kill` run scoped to that package. This focus scoping is
  a rigsmith addition beyond both source menus.

## `rig cd` — project picker (huh single-select)
- **Trigger:** `rig cd` with no query on a TTY, **or** `rig cd <query>` whose
  fuzzy match is ambiguous on a TTY.
- **What you see:** a huh select titled `cd to which project?`, listing
  `<name>  (<relative path>)` per project (plus `(root)`).
- **What it does:** prints the chosen absolute dir to **stdout** (prompts go to
  stderr); the `rig()` shell wrapper installed by `rig setup` captures it and
  `cd`s the parent shell.
- **Non-TTY:** no query → prints the repo root (success); ambiguous query →
  prints the candidate list to stderr and exits non-zero.

## `rig default` — set-default picker (huh single-select)
- **Trigger:** bare `rig default` on a TTY (with >1 runnable project), or
  `rig default <query>` with an ambiguous match on a TTY.
- **What you see:** a huh select titled `Set default project` (or
  `Which project?`) over the runnable project names.
- **What it does:** persists `defaultProject = <name>` into `.rig.json`
  (comment-preserving) and prints where it landed.
- **Non-TTY:** prints the current default (or "No default project set.").

## Coverage ReportGenerator download prompt (huh single-select)
- **Trigger:** `rig coverage` on a TTY when ReportGenerator isn't installed,
  `dnx` is available to fetch it, mode is `auto`, and not `--quiet`/`--dry-run`.
- **What you see:** title `ReportGenerator isn't installed — it renders a richer
  coverage report.`, description `Download it on demand (via dnx)?`, three
  options:
  - `Yes — download and use it from now on` → downloads now, persists
    `coverage.reportGenerator = download` to `.rig.json`.
  - `Not now — use the basic report` → one-time native report; asks again next time.
  - `Never ask again` → persists `coverage.reportGenerator = off`.
- **Non-TTY / interrupt:** silently uses the native report (defaults to "not now").

## `rig coverage` — per-file summary table (lipgloss)
- **Trigger:** automatically after a `rig coverage` run on an interactive stdout
  (not `--quiet`/`--dry-run`). `--no-summary` opts out.
- **Why:** the terminal-native counterpart to `--open` (which launches an HTML
  report in a browser) — see what needs attention without leaving the shell.
- **What you see:** a `Coverage summary` table, **one row per file sorted
  worst-covered first**, each with a 10-cell block bar and a line-% colored by
  band (green ≥80, yellow ≥50, red below), then a bold `TOTAL (N files)` row.
  Long file paths are left-ellipsized (the tail survives); lists over 40 files
  collapse the rest into a `… N more file(s)` line.
- **Where the numbers come from:** whatever the run already produced — Cobertura
  for .NET, the `-coverprofile` for go (a throwaway temp profile when `--open`
  isn't set), `coverage/coverage-summary.json` for node — so it never costs an
  extra command. For vitest, the `json-summary` reporter is requested
  automatically so the table can be drawn; non-vitest node runners only get a
  table when they already emit `coverage-summary.json`.
- **Best-effort:** if no readable artifact exists the table is silently skipped;
  it's a convenience, never a failure. Independent of `--min` (the gate still
  prints its own verdict) and `--open`.

## `rig coverage --browse` — per-file/per-line browser (bubbletea + viewport)
- **Trigger:** `rig coverage --browse` (`-b`) on an interactive terminal — the
  in-terminal counterpart to `--open` (which renders the same thing as HTML in a
  browser).
- **What you see:** two views in one alt-screen program.
  - **List:** the files (worst-covered first), each a selectable row with the
    block bar and colored line-% — the summary table with a cursor. `↑/↓`/`j/k`
    move, `g/G` jump to ends, `enter`/`→` opens the selected file.
  - **Detail:** the file's source in a scrollable viewport, each line prefixed
    with its number, a covered (`│` green) / uncovered (`✗` red, line tinted) /
    non-executable (blank) marker and the hit count. `↑/↓`/pgup/pgdn scroll,
    `esc`/`←` returns to the list. `q`/`ctrl+c` quits from either view.
- **Where the per-line data comes from:** Cobertura (.NET), the `-coverprofile`
  (go — a throwaway temp profile when `--open` isn't set), or **`lcov.info`**
  (node — note: per-line needs lcov, not `coverage-summary.json`; rig requests
  the lcov reporter for vitest automatically). Source files are resolved on disk
  (the report's `<sources>` for .NET, the workspace module map for go, lcov's
  `SF:` for node); a file whose source can't be located still shows its bare
  line→hits ledger.
- **Fallback:** when no per-line data can be assembled (e.g. a node runner that
  emits neither lcov nor a summary), `--browse` falls back to the static summary
  table. Uses the alt-screen, so it leaves no clutter in scrollback on exit.

## `rig doctor` — live checklist (bubbletea + bubbles)
- **Trigger:** `rig doctor` on an interactive terminal (not `--quiet`/piped).
  Otherwise the static checklist prints.
- **Why:** the toolchain checks shell out (`go version`, `node --version`,
  `dotnet --version`, …), so a live view that spins each row until its probe
  resolves feels responsive instead of a frozen pause.
- **What you see:** **workspace-aware** — it discovers every project (the shared
  `discoverWorkspace` searcher) and shows, **grouped under an ecosystem header**
  (`Go` / `Node` / `.NET` / `Cargo`): the toolchain rows once (tool version, SDK
  + any global.json pin), then **one row per project** with its own state — node
  deps (`deps installed` / `deps declared, not installed — run rig install` /
  `no dependencies`), .NET target framework, go/cargo versions — and the
  project's **repo-relative path** in a trailing dim column (so it's clear where
  each project lives). Each row spins `checking…` then resolves to `✓`/`!`/`✗`;
  checks run **concurrently** (each a bubbletea Cmd). A final verdict: `all good`
  / `some warnings` / `problems found`. (Toolchain rows carry no path.)
- **What it does:** read-only — exits non-zero only when an error-level check
  fails (warnings don't), so it still doubles as a CI gate (via the static
  path). `q`/`ctrl+c` quits early. Renders inline, so the result stays in
  scrollback.

## `rig kill` — review-and-select (huh multi-select)
- **Trigger:** `rig kill` / `rig kill <name>` / `rig kill --port N` on an
  interactive terminal, not `--dry-run` and not `--yes`. The default — killing
  is destructive, so review before it happens.
- **What you see:** the matched processes (`<pid>  <command line>`) in a
  multi-select, **all pre-checked**. Title: `Kill which processes? (space toggles
  · enter confirms · esc cancels)`. Uncheck any you want to spare.
- **What it does:** kills the PIDs you keep (`kill -TERM` / `taskkill /F /T`) and
  reports the count. Esc/empty selection kills nothing.
- **Bypass / fallback:** `--yes`/`-y` skips the picker and kills every match (the
  old behavior); off a TTY (CI/piped) it likewise kills all matches without
  asking. `--dry-run` still just lists. Matches come from the same enumeration as
  before — project-name patterns (`pgrep -fl` / CIM) or listening PIDs by port.

## `rig outdated -i` — interactive upgrade (huh multi-select)
- **Trigger:** `rig outdated -i`/`--interactive` on an interactive terminal (not
  `--dry-run`). Plain `rig outdated` is unchanged — it streams the ecosystem's
  outdated report as before.
- **What you see:** rig runs the ecosystem's machine-readable outdated report,
  parses it, and shows the outdated packages (`name  current → latest`, .NET also
  tags the owning project) in a multi-select titled `Upgrade which packages?
  (space toggles · enter confirms · esc cancels)` — **nothing pre-checked**, you
  opt packages in. "All dependencies are up to date 🎉" when there's nothing to do.
- **What it does:** upgrades the packages you pick, echoing each command —
  **go** `go get pkg@latest …` then `go mod tidy`; **node** `npm install` /
  `pnpm add` with `name@latest` specs (**bun** `bun add` / `bun add --dev`,
  split so dev deps stay dev; **yarn classic** `yarn upgrade --latest`); **.NET**
  `dotnet add [project] package id --version latest` per package. Esc/empty
  selection upgrades nothing.
- **Support / fallback:** wired for **go**, **node (npm/pnpm/bun/yarn)**, and
  **.NET**. **Yarn Berry** has no machine-readable outdated, so rig hands off to
  its built-in interactive upgrader (`yarn up -i`) instead of the rig picker.
  Unrecognized ecosystems or an unparseable report fall back to the plain list;
  off a TTY, `-i` prints a hint and lists.
- **Data sources:** `go list -m -u -json all` (go); `<pm> outdated --json`
  (npm/pnpm — npm exits non-zero with valid JSON, which rig tolerates); `bun
  outdated` (bun has no `--json`, so rig parses its pipe-delimited ASCII table,
  preserving the `(dev)` tag); `yarn outdated --json` (yarn classic — NDJSON,
  rig reads the `table` row); `dotnet list package --outdated --format json`
  (.NET). The yarn-classic / bun upgrades use `yarn upgrade --latest` /
  `bun add [--dev]`, which keep each package in its existing section.

## `rig <verb>` at a workspace root — project picker (huh single-select)
- **Trigger:** a bare dev verb at a workspace root where packages live only in
  subdirs (e.g. a `go.work` root) and several exist — running the verb at the
  root has no single target. Applies to the `--all`-capable verbs
  (`build`/`test`/`format`/`lint`/`typecheck`/`clean`) **and `run`**.
- **What you see:** a select titled `Build which?` / `Run which?` (verb-specific)
  listing each package (`name  (path · ecosystem)`).
  - For `--all`-capable verbs, **`All packages`** leads the list (→ the `--all`
    dashboard).
  - For **`run`** (which has no `--all`), there's **no "All packages"** entry —
    it's a single pick, and the list is filtered to **runnable** packages (a Go
    module with no `package main` is omitted, so libraries don't clutter it). A
    lone runnable package is run directly without a prompt.
- **Non-TTY:** a helpful error — `no single build target here … run rig build
  --all or rig build <project>` (the `--all` hint is dropped for `run`).
  Single-package repos (or a package at the root) are unaffected: the normal
  root command runs.

## `--list-tests` discovery spinner
- **Trigger:** during `rig test <query>` on a .NET repo, while
  `dotnet test --list-tests` builds + enumerates (slow).
- **What you see (TTY):** a braille spinner cycling at ~80ms with the label
  `Discovering tests (dotnet test --list-tests)`, on stderr, cleared when done.
- **Non-TTY:** a single `…` status line (or nothing under `--quiet`).

## `rig <verb> --all` — live workspace dashboard (bubbletea + bubbles)
- **Trigger:** a dev verb with `--all` (`rig build --all`, `rig test --all`, …)
  on an interactive terminal (not `--quiet`/`--dry-run`/piped). Otherwise the
  plain sequential path streams each package as before.
- **Why:** running a verb across a polyglot monorepo is exactly a per-row live
  status display — see what's building, what passed, what failed, at a glance.
- **What you see:** `── <verb> --all ──` then one row per runnable package (topo
  order) with a live status glyph — `○` pending, a spinner while running, `✓` ok
  (green), `✗` failed (red), `–` skipped. Under the running package its output
  streams in (last ~8 lines, dim). Footer while running: `ctrl+c cancel`; on
  finish: `✓ N ok   ✗ M failed   (cancelled)`.
- **What it does:** runs each package's command sequentially in dependency order
  (a goroutine feeds the program), **continuing through failures** so you see the
  full picture (the sequential path still aborts on the first failure). Exits
  non-zero if any package failed. `ctrl+c`/`q` cancels the remaining packages and
  kills the running command (context cancellation); a clean cancel exits 0.
  Renders inline (no alt-screen), so the final state stays in scrollback.
- **Note:** output is captured (combined stdout+stderr) to stream it into the
  rows, so child-process TTY coloring is lost — the tradeoff for the live view.

## `rig setup` — shell-integration installer (not interactive)
- **Trigger:** `rig setup [shell]`. Detects the shell, splices an idempotent
  marker block (the `rig()` cd wrapper + completion sourcing) into the rc file
  (zsh/bash/fish; PowerShell prints). `--print` shows the snippet without writing.
- **What you see:** `Installed rig shell integration (cd wrapper + completion) in
  <path>` (or "already installed … nothing to do"). No prompts.

---

# `changerig` (changeset lifecycle, `changerig/`)

## `changerig add` — changeset form (huh form)
- **Trigger:** `changerig add` with **no** content flags. Providing any of
  `-m/--message`, `--bump`, `-t/--type`, `-p/--package` (or `--empty`) skips the
  form entirely and runs non-interactively.
- **What you see:** a two-group huh form —
  1. multi-select `Which packages are affected?` over the discovered package names;
  2. select `Bump type for these packages` with options
     `patch — bug fixes` / `minor — new features` / `major — breaking changes`
     (defaults to patch), then a text field `Summary` with placeholder
     `Describe the change for the changelog`.
- **`--since <ref>`:** pre-checks the packages owning files changed since that
  git ref (it preselects in the picker; it does not skip the form).
- **What it does:** writes `.changeset/<human-id>.md` from the answers; with
  `--open`, opens it in `$EDITOR`; with the `commit` config key, commits it.

## `changerig ui` — verb menu (bubbletea)
- **Trigger:** `changerig ui` (and `relrig ui` — shared command).
- **What you see:** header `<root>  ·  <N> package(s)  ·  <M> pending
  changeset(s)`, title `relrig`/`changerig`, and the entries
  `Status` / `Add changeset` / `Browse changesets` / `Version` / `Info` with dim
  descriptions. Cursor `▸` + bold-cyan selection; hint `↑/↓ move · enter select
  · q quit`.
- **What it does:** runs the chosen verb immediately on selection.

## `changerig browse` — changeset browser/manager (bubbletea + viewport)
- **Trigger:** `changerig browse` (aliases `ls`/`list`, also in the `ui` menu)
  on an interactive terminal. Off a TTY it prints a plain one-line-per-changeset
  list instead.
- **What you see:** two views in one program.
  - **List:** `Changesets (N)` with one selectable row per pending changeset —
    a bump/type **badge** (highest explicit release bump, else the conventional
    type, else `auto`; colored major=red/minor=yellow/patch=green), the
    changeset id, its packages, and the summary's first line (truncated to
    width). Cursor `▸`.
  - **Detail:** the selected changeset's `Releases` (each package + bump, `auto`
    when derived from type), `Type` (with a breaking marker), and the full
    `Summary` in a scrollable viewport.
- **Manage:** `d` deletes the selected changeset (inline `y/n` confirm, removes
  the file, refreshes the list); `e` opens it in `$VISUAL`/`$EDITOR` (suspending
  the UI via `tea.ExecProcess`, re-reading on return). A transient status line
  reports the last action (e.g. `deleted <id>`).
- **Keys:** `↑/↓`/`j/k` move, `g/G` ends, `enter`/`→` view, `esc`/`←` back, `d`
  delete, `e` edit, `q`/`ctrl+c` quit. Empty state points at `changerig add`.

## `changerig init` — not interactive
Writes `.changeset/config.json` + README and prints
`Initialized changesets in <path>` (or a benign `Already initialized …`). No
prompts (deliberate — see the gap analysis).

## `status` / `version` plan — styled, non-interactive
`PrintPlan` renders the release plan: per package a bump-colored label
(major=red, minor=yellow, patch=green), `name`, and `current → new`; with
`--verbose`, dim bullet lines per change. Styled output, not interactive.

---

# `relrig` (release tool, `release/`)

`relrig` reuses changerig's `add`/`ui`/`status`/`version` commands verbatim, so
those UIs are identical. Its own surfaces:

## `publish` confirm gate (huh confirm)
- **Trigger:** `relrig publish` (and `changerig publish`) just before the first
  network side-effect (registry push / tag push), on a TTY, when not `--dry-run`
  and not `--yes`.
- **What you see:** a huh confirm `Publish <N> package(s) to their registries
  (and push tags)?`.
- **What it does:** declining prints `Publish cancelled.` and exits cleanly.
- **Non-TTY / `--yes`:** proceeds without prompting (the CI path).

## `relrig release` — confirm gates (huh confirm)
- **Trigger:** any pipeline step whose `.changeset/release.jsonc` config sets
  `confirm: true` or `confirm: "<message>"`, on a TTY, unless `--yes`.
- **What you see:** a huh confirm with the step's message (default
  `Proceed with the '<step>' step?`).
- **What it does:** declining stops the run at that step; the reporter prints a
  cancellation with a `--only <step>` resume hint. Off a TTY the gate resolves
  via a fixed answer (proceed under `--yes`, otherwise halt) — never hangs.

## `relrig release` — reporters (plain vs rich)
The pipeline is headless; everything renders through a `Reporter`. Mode is
auto-by-TTY, forced with `--ui`/`--no-ui` (and `--yes` implies plain):
- **Plain** (piped / `--no-ui`): `Release plan:` followed by `==> step`,
  `$ command`, indented output, `ok step` / `x step failed (exit N)`,
  `Release complete.` / `Release failed. <message>` + resume hint. Pure text.
- **Rich** (TTY default / `--ui`): the same event stream with lipgloss rules
  (`── Release plan ──`, `── <step> ──`), green `run`/`ok`, and boxed
  success/cancel/failure panels (green/orange/red borders) with the resume hint.
- Both run **secret-masked** — captured `vars` values are redacted in every line;
  `--dry-run` never captures, so the printed plan stays literal.

The above three describe the **sequential** path (CI, `--yes`, piped, `--no-ui`,
`--dry-run`). On an interactive rich TTY a real run instead uses the full TUI
flow below.

## `relrig release` — plan editor (bubbletea, pre-run)
- **Trigger:** an interactive, rich, non-dry-run `relrig release` (a TTY, not
  `--yes`/`--no-ui`/piped). The `interactiveChooser` PlanChooser implementation.
- **Why:** review the resolved plan and choose which steps run before anything
  executes — the faithful port of the source's interactive step picker.
- **What you see:** `── Release plan — choose steps ──`, then each step as
  `▸ [x] <name>` (cursor + checkbox), with the cursor step's action commands
  shown dim beneath it, a dim `(reason)` on flag-skipped steps, and a `⏸ confirm`
  tag on gated steps. Footer: `↑/↓ move · space toggle · a all · n none · enter
  run · q cancel`.
- **Keys:** `↑/↓`/`k`/`j` move, `space`/`x` toggle a step, `a` enable all,
  `n` disable all, `enter`/`g` run with the current selection, `q`/`esc`/`ctrl+c`
  cancel the whole release.
- **What it does:** toggled-off steps get `SkipReason = "disabled in plan editor"`
  (toggling a flag-skipped step back on re-enables it); the result feeds the run.
  Cancelling prints `Release cancelled.` and exits without running anything.

## `relrig release` — live dashboard (bubbletea, during the run)
- **Trigger:** the same interactive-rich-real-run path, immediately after the
  editor. The headless pipeline runs in a goroutine and drives this single
  bubbletea program through a `Reporter`→`tea.Msg` bridge (`dashReporter`).
- **What you see:** `── Release ──`, then the step list with live per-step status
  glyphs — `○` pending, a spinner while running, `✓` ok (green), `✗` failed
  (red), `⊘` cancelled (orange), `–` skipped (dim). Under the running step, the
  current `$ command` and the last few output lines stream in (masked). On
  completion, the same success/fail/cancel panels + resume hint as the rich
  reporter.
- **Confirm gates inline:** when a step is gated, the dashboard shows
  `⏸ <message>  [y/N]` and the pipeline blocks (via `dashPrompter`) until you
  answer — so one program owns the terminal the whole run (no separate huh
  prompt). `y`/`enter` proceeds; `n`/`esc` declines and stops cleanly.
- **What it does:** runs the chosen steps. **`ctrl+c` is intentionally ignored
  while a step is executing** — a release can't be safely torn down mid-command
  (a half-published package); decline at the next confirm gate to stop. The
  program renders inline (no alt-screen), so the final state stays in scrollback.

---

# Quick reference

| Surface | Tool | Toolkit | Trigger | Off-TTY fallback |
|---|---|---|---|---|
| Main menu (`ui`/bare) | rig | bubbletea | `rig ui` / bare `rig` | fails fast |
| Project focus picker | rig | bubbletea (menu frame) | >1 project, in menu | n/a (in menu) |
| `cd` picker | rig | huh select | `rig cd` (TTY, ambiguous/bare) | print root / list+fail |
| `default` picker | rig | huh select | `rig default` (TTY, ambiguous/bare) | print current |
| Coverage RG prompt | rig | huh select | RG absent, TTY, dnx, auto | native report |
| Coverage summary table | rig | lipgloss | after `rig coverage` (TTY, not `--no-summary`) | skipped |
| Coverage browser | rig | bubbletea + viewport | `rig coverage --browse` (TTY) | static table |
| `doctor` live checklist | rig | bubbletea + bubbles | `rig doctor` (TTY) | static checklist |
| `kill` review-and-select | rig | huh multi-select | `rig kill` (TTY, not `--yes`) | kill all matches |
| `outdated -i` upgrade | rig | huh multi-select | `rig outdated -i` (TTY) | plain list |
| workspace-root picker | rig | huh select | bare verb at a multi-pkg root (TTY) | helpful error |
| `--list-tests` spinner | rig | lipgloss anim | `rig test <q>` (.NET) | `…` line / silent |
| `<verb> --all` dashboard | rig | bubbletea + bubbles | `rig build/test --all` (TTY) | plain sequential |
| `setup` | rig | none (file I/O) | `rig setup` | (not interactive) |
| `add` form | changerig/relrig | huh form | `add`, no flags | provide flags |
| `ui` menu | changerig/relrig | bubbletea | `changerig ui` | fails fast |
| `browse` changesets | changerig/relrig | bubbletea + viewport | `changerig browse` (TTY) | plain list |
| `publish` confirm | changerig/relrig | huh confirm | network side-effects (TTY) | proceed w/ `--yes` |
| `release` confirm gates | relrig | huh confirm | `confirm:` step (TTY) | fixed answer |
| `release` reporters (sequential) | relrig | lipgloss (rich) / text (plain) | non-interactive / piped / `--no-ui` / dry-run | plain |
| `status`/`version` plan | changerig/relrig | lipgloss (styled) | always | (styled text) |
| `release` plan editor | relrig | bubbletea | interactive rich real run | passthrough |
| `release` live dashboard | relrig | bubbletea + bubbles | interactive rich real run | sequential reporter |

---

# Gap analysis vs the .NET / Node sources

The sources used **Spectre.Console** (net-changesets) and the .NET/Node `rig`
menus; rigsmith reimplements the same surfaces on bubbletea/huh. Status of each
interactive surface:

### At or above parity (done)
- **Verb menus** — net-changesets' Spectre `ui` and the .NET/Node `rig` menus →
  the bubbletea menus here. rig's menu goes **beyond** both with the project
  **focus scoping** + breadcrumb (dev verbs + kill run scoped).
- **Interactive `add`** — the package-picker + bump-selector + summary form,
  matched (and extended with `--type`/`--since`/`--open`).
- **Release confirm gates + reporters** — the `confirm:` huh gates and the
  plain/rich (lipgloss) reporters mirror the source's `TuiReleaseReporter`
  rendering (plan table, per-step rules, masked output, success/cancel/failure
  panels, resume hints).
- **Publish confirm**, **cd/default pickers**, **coverage download prompt**,
  **list-tests spinner** — rigsmith surfaces (the pickers/spinner have no direct
  Spectre equivalent; they're rig-side ergonomics).

### Done — the relrig step-chooser TUI
The source's interactive **step picker** (toggle which steps run before the
release starts) is now built as the **plan editor** above, and goes further with
a **live run dashboard** (streaming per-step status + inline confirm gates) on
top of the same headless pipeline. The `PassthroughChooser` remains the
non-interactive fallback. This was the last real interactive-UI gap.

### Remaining (accepted divergences, optional)
1. **C#-style interactive config walkthrough** — *accepted divergence, optional.*
   net-changesets' `init` and the .NET `rig setup` ran an interactive
   config-prompt wizard (sourcePath/packageSource/interop for changesets; rig
   config for setup). rigsmith deliberately split this: `changerig init` writes a
   sensible default config (the dropped prompts — interop/sourcePath — don't
   apply to the Go design), and `rig setup` became the **shell installer**
   instead, with `rig default` covering the one config a wizard would still set.
   A guided first-run wizard could be added if wanted, but nothing functional is
   missing.
2. **Interactive `rig default` as its own verb** — minor. The picker described
   above exists and persists; this is only noting that the source exposed a
   dedicated interactive config verb, which here is folded into `default`/`init`.

**Bottom line:** the interactive-UI surface is now at parity — the relrig
step-chooser TUI (plan editor + live dashboard) is built. What remains is the
optional C#-style config wizard, an intentional, documented divergence.
