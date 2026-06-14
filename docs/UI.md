# Interactive UI surface — rigsmith

What interactive / terminal UI exists today across the three tools (`rig`,
`changerig`, `shiprig`), when each appears and why, what the user sees, and what
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

**Bare invocation → the menu.** Run truly bare (no verb, no flags) on a TTY,
each tool lands on its interactive hub — `rig`/`changerig`/`shiprig` open their
menu, `clauderig` its dashboard — with a context-aware **next step** in view
(and, for the verb menus, pre-selected): `init` when nothing's set up, `add`
when there's nothing to release, `status`/`version` when changesets are pending.
With *any* arg or flag, or off a TTY, the prior behavior stands — help for
`rig`/`clauderig`, `status` for `shiprig`, `add` for `changerig` — so scripts,
hooks, and `-h` are unchanged. `rig`/`clauderig` route through their `ui` verb
(not a root `RunE`), so cobra's unknown-command errors are preserved.

**Context-aware menus.** Every hub reflects current state, not a fixed list — it
shows only what's actionable now. `rig` already hides verbs the ecosystem can't
map and adds a `▸ Project commands` group for configured commands/scripts;
`changerig`/`shiprig` gate the lifecycle by source mode + pending changesets +
prerelease (and shipRig adds its release verbs); `clauderig` offers `sync` only
with a remote and `restore` only with a snapshot to pull. The legend/menu lists
only the available actions, so a verb that would just error never appears.

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
- **Trigger:** `rig ui`, or bare `rig` with no verb/flag on a TTY (which routes
  to `ui`). First resolves the repo root + primary ecosystem; on a TTY an
  ambiguous ecosystem opens the picker rather than erroring.
- **Why:** a discoverable, capability-aware launcher for the everyday verbs.
- **What you see:** a header (`<root>  ·  <primary ecosystem>`); when there's no
  `.rig.json` yet, a green `→` **next-step** line and a top **`init`** entry
  tagged `next` (it pins conventions and is where custom verbs live). Then a
  grouped list: the dev verbs (build/test/run/format/lint/typecheck); a
  `▸ Dependencies` submenu (install/ci/outdated/upgrade); a `▸ Maintenance`
  submenu (clean, coverage, kill, doctor, self-update). The cursor is a cyan
  `▸`; the selected label is bold cyan; descriptions are dim. A breadcrumb shows
  the path (`rig`, `rig · <project>`, `rig / Maintenance`); the next-step line
  shows only at the top level. Hint line:
  `↑/↓ move · enter select · esc back · q quit`.
- **Capability probing:** only verbs the primary ecosystem actually maps are
  shown. For .NET it probes the repo (no test project → no test/coverage; no
  runnable project → no run). `kill`/`doctor` always appear.
- **Configured commands:** when the repo defines them, a **`▸ Project commands`**
  submenu lists this repo's `.rig.json` custom commands and discovered scripts
  (package.json scripts, Go `scripts/*/cmd` verbs) — the same set `rig <name>`
  runs, deduped with the same precedence (custom > package.json > Go). Each runs
  its own prebuilt command on selection. The group is omitted when there are none.
- **Keys:** `↑/↓`/`k`/`j` move, `enter`/`l`/`→` select (open submenu / pick
  focus / run), `esc`/`backspace`/`h`/`←` back, `q`/`ctrl+c` quit.
- **What it does:** selecting a verb quits the menu and dispatches it (routing to
  the standalone command — coverage/doctor/kill/self-update — or the generic
  ecosystem verb), scoped to the focused project if one is set.
- **Non-TTY:** bare `rig` off a TTY prints help (the prior behavior), so scripts
  and `rig | …` are unchanged; explicit `rig ui` off a TTY still fails fast
  rather than hanging.

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

## Primary-ecosystem picker (huh select + confirm)
- **Trigger:** any verb that resolves the primary ecosystem (`build`/`test`/`run`/
  …, `coverage`, `outdated`, `upgrade`, `ui`, …) in a repo where **several
  ecosystems coexist at the same level and no `.rig.json` pins one** — the case
  that used to hard-error with "multiple ecosystems found…". On a TTY only.
- **What you see:** a select titled `Which ecosystem should rig use here?` over
  the coexisting ecosystems (display names), then a confirm `Remember this in
  .rig.json?` (default yes).
- **What it does:** the chosen ecosystem is used for the run; if you keep
  "remember", it's persisted as `"ecosystem"` in `.rig.json` (comment-preserving,
  created if absent) and a dim `set ecosystem = <id> in .rig.json` note prints.
  Either way the choice is **cached per repo root for the rest of the process**,
  so a single command never asks twice (and `ui` → verb don't double-prompt).
- **Non-TTY / cancel:** unchanged behavior — the `multiple ecosystems found (…)
  — set "ecosystem" in .rig.json` error, so CI stays deterministic. Pairs with
  the `rig init` wizard, which sets the same key up front.

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

## `rig <verb>` at a workspace root — project / run picker
- **Trigger:** a bare dev verb at a workspace root where packages live only in
  subdirs (e.g. a `go.work` root) and there's no single target. Covers the
  `--all`-capable verbs (`build`/`test`/`format`/`lint`/`typecheck`/`clean`) and
  `run`. `--pick`/`-p` forces the picker even when one obvious target exists.
- **`--all`-capable verbs — huh single-select:** titled `Build which?`, with
  **`All packages`** first (→ the `--all` dashboard) then each package
  (`name  (path · ecosystem)`). A lone subpackage just runs (no prompt).
- **`run` — grouped bubbletea picker:** titled `Run which?`, with two aligned,
  column-matched groups: **Projects** (the runnable packages — a Go module with
  no `package main` is filtered out) and **Scripts** (the repo's surfaced
  scripts: `package.json` scripts, `.rig.json` commands, Go `scripts/*/cmd`
  verbs — the same ones `rig <name>` runs). No "All packages"; `↑/↓ move · enter
  select · q quit`, drawn on stderr so stdout stays clean for the chosen command.
- **Non-TTY:** a helpful error (`no single build target here … rig build --all or
  rig build <project>`; the `--all` hint is dropped for `run`). A package at the
  root (or a single-package repo) runs the normal root command.

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

## `rig init` — config wizard (huh form)
- **Trigger:** `rig init` on an interactive terminal with no existing
  `.rig.json`. `--yes`/`-y` (or a non-TTY) writes the plain scaffold instead;
  an existing `.rig.json` is never overwritten.
- **What you see:** a `Detected: …` summary line (per-ecosystem counts, e.g.
  `3 Go modules · 2 Node packages · 1 .NET project`) above a short huh form,
  seeded from what's detected:
  - **Primary ecosystem** — a select of the ecosystems actually present (plus
    `Auto-detect (don't pin)`), defaulting to the nearest one. This is the value
    `resolvePrimary` reads to disambiguate a polyglot repo.
  - **Solution** — shown only when several `.sln`/`.slnx` exist; a select (with
    `(auto)`) of which one rig builds/discovers against.
  - **Default project** — shown only when there are several runnable .NET
    projects; a select (with `(none)`) of their short names.
  - **Exclude from discovery?** — shown only when sample/example-ish dirs
    (`examples`/`samples`/`fixtures`/`testdata`/`demo`/`e2e`/…) actually hold
    packages; a multi-select to keep them out of `--all`/`doctor`.
  - **Quiet by default?** — a confirm that sets `quiet` (suppress the `→ command`
    echo).
- **What it does:** writes a `.rig.json` scaffold (all keys shown) with the
  chosen `ecosystem` / `solution` / `defaultProject` / `exclude` / `quiet` filled
  in. Esc/ctrl+c cancels without writing. The conditional fields only appear when
  the repo gives them something to choose, so a simple repo still sees just
  ecosystem + quiet.
- **Non-TTY / `--yes`:** the original behavior — the plain scaffold with empty
  defaults.

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

## `changerig add` — inline setup offer (huh confirm)
- **Trigger:** `add` (or `changerig -m …`/`--source …` — any flag keeps the bare
  tool out of the menu) in a workspace with **no `.changeset/` yet**, on an
  interactive terminal. (Truly bare `changerig` on a TTY opens the menu instead,
  whose `Initialize` entry covers the same setup.) Instead of erroring out, it
  offers to set changesets up right there.
- **What you see:** a dim line `No changesets set up in <root> yet.`, then a huh
  confirm `Set up changesets here?` with description `Creates .changeset/ with a
  default config.`. Accepting scaffolds the folder/config (the same
  `Scaffold` `init` uses), prints a green `✓ Initialized changesets in
  .changeset/`, and drops straight into the add form. Declining (or esc/ctrl+c)
  prints `No changeset created. Run `changerig init` when you're ready.` and
  exits cleanly (no error).
- **Non-TTY:** can't prompt, so it fails with a clear, actionable error —
  `changesets aren't set up here yet — run `changerig init` to create
  .changeset/` (exit non-zero, the CI path).

## `changerig ui` — verb menu (bubbletea)
- **Trigger:** `changerig ui` / `shiprig ui` (shared command), or bare
  `changerig`/`shiprig` (no verb/flag) on a TTY.
- **State-driven — shows only the verbs that currently apply.** The menu is
  built from the workspace's live state, so it never offers a dead-end verb:
  - **Uninitialized:** header `<root>  ·  not set up`; just **`Initialize`**
    (tagged `next`) and `Info`.
  - **Initialized:** header `<root>  ·  <N> package(s)  ·  <M> pending
    changeset(s)`, then `Status` (always); `Add changeset` only in changeset
    mode (changesets/both); `Browse changesets` only when changesets are pending;
    `Version` only when there's something to release (pending changesets, or
    commit mode); then any tool extras (see shipRig below); `Info` (always).
  - **Prerelease active:** the header gains a `· prerelease <tag>` badge and an
    **`Exit prerelease (<tag>)`** entry appears.
- **Next step:** a green `→` line names the suggested action, which is
  pre-selected and tagged `next` — `Status` when changesets are pending (or in
  commit/prerelease mode), else `Add changeset` (or `Initialize` when unset).
- **Look:** title `shiprig`/`changerig`, cursor `▸` + bold-cyan selection, dim
  descriptions; hint `↑/↓ move · enter select · q quit`.
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

# `shiprig` (release tool, `shiprig/`)

`shiprig` reuses changeRig's `add`/`ui`/`status`/`version` commands verbatim, so
those UIs are identical. Bare `shiprig` (no verb/flag) on a TTY opens that shared
menu; with any arg/flag or off a TTY it stays `status` (the "what would I ship?"
answer CI and pipes rely on). shipRig **contributes its release verbs**
(`Publish` / `Tag` / `Release`) to the shared menu via `commands.NewUICmd(extra…)`,
so the menu reflects the release tool's full surface, not just the inherited
lifecycle. Its own surfaces:

## `publish` confirm gate (huh confirm)
- **Trigger:** `shiprig publish` (and `changerig publish`) just before the first
  network side-effect (registry push / tag push), on a TTY, when not `--dry-run`
  and not `--yes`.
- **What you see:** a huh confirm `Publish <N> package(s) to their registries
  (and push tags)?`.
- **What it does:** declining prints `Publish cancelled.` and exits cleanly.
- **Non-TTY / `--yes`:** proceeds without prompting (the CI path).

## `shiprig release` — confirm gates (huh confirm)
- **Trigger:** any pipeline step whose `.changeset/release.jsonc` config sets
  `confirm: true` or `confirm: "<message>"`, on a TTY, unless `--yes`.
- **What you see:** a huh confirm with the step's message (default
  `Proceed with the '<step>' step?`).
- **What it does:** declining stops the run at that step; the reporter prints a
  cancellation with a `--only <step>` resume hint. Off a TTY the gate resolves
  via a fixed answer (proceed under `--yes`, otherwise halt) — never hangs.

## `shiprig release` — reporters (plain vs rich)
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

## `shiprig release` — plan editor (bubbletea, pre-run)
- **Trigger:** an interactive, rich, non-dry-run `shiprig release` (a TTY, not
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

## `shiprig release` — live dashboard (bubbletea, during the run)
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
| Main menu (`ui`/bare) | rig | bubbletea | `rig ui`, or bare `rig` (TTY) | help (`rig ui`: fails fast) |
| Project focus picker | rig | bubbletea (menu frame) | >1 project, in menu | n/a (in menu) |
| `cd` picker | rig | huh select | `rig cd` (TTY, ambiguous/bare) | print root / list+fail |
| `default` picker | rig | huh select | `rig default` (TTY, ambiguous/bare) | print current |
| Coverage RG prompt | rig | huh select | RG absent, TTY, dnx, auto | native report |
| Coverage summary table | rig | lipgloss | after `rig coverage` (TTY, not `--no-summary`) | skipped |
| Coverage browser | rig | bubbletea + viewport | `rig coverage --browse` (TTY) | static table |
| `doctor` live checklist | rig | bubbletea + bubbles | `rig doctor` (TTY) | static checklist |
| `kill` review-and-select | rig | huh multi-select | `rig kill` (TTY, not `--yes`) | kill all matches |
| `outdated -i` upgrade | rig | huh multi-select | `rig outdated -i` (TTY) | plain list |
| workspace-root picker | rig | huh select (`run`: bubbletea, grouped projects+scripts) | bare verb / `--pick` at a multi-pkg root (TTY) | helpful error |
| primary-ecosystem picker | rig | huh select + confirm | ambiguous ecosystem, no pin (TTY) | "set ecosystem" error |
| `--list-tests` spinner | rig | lipgloss anim | `rig test <q>` (.NET) | `…` line / silent |
| `<verb> --all` dashboard | rig | bubbletea + bubbles | `rig build/test --all` (TTY) | plain sequential |
| `init` wizard | rig | huh form | `rig init` (TTY, no `.rig.json`) | plain scaffold |
| `setup` | rig | none (file I/O) | `rig setup` | (not interactive) |
| `add` form | changeRig/shipRig | huh form | `add`, no flags | provide flags |
| `add` setup offer | changeRig/shipRig | huh confirm | `add` in an uninit workspace (TTY) | clear `init` error |
| `ui` menu | changeRig/shipRig | bubbletea | `changerig ui`/`shiprig ui`, or bare (TTY) | `status` (ship) / `add` (change) |
| `browse` changesets | changeRig/shipRig | bubbletea + viewport | `changerig browse` (TTY) | plain list |
| `publish` confirm | changeRig/shipRig | huh confirm | network side-effects (TTY) | proceed w/ `--yes` |
| `release` confirm gates | shipRig | huh confirm | `confirm:` step (TTY) | fixed answer |
| `release` reporters (sequential) | shipRig | lipgloss (rich) / text (plain) | non-interactive / piped / `--no-ui` / dry-run | plain |
| `status`/`version` plan | changeRig/shipRig | lipgloss (styled) | always | (styled text) |
| `release` plan editor | shipRig | bubbletea | interactive rich real run | passthrough |
| `release` live dashboard | shipRig | bubbletea + bubbles | interactive rich real run | sequential reporter |

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
- **Expanded rig-side interactive surface** (no source equivalent — net-new
  ergonomics): the **`--all` live dashboard**, **`kill` review-and-select**,
  **`doctor` live checklist** (per-project rows + paths), **`coverage` summary
  table + `--browse`**, **`outdated -i` upgrade**, **`changerig browse`** changeset
  manager, the **workspace-root / `run` pickers**, and the **primary-ecosystem
  picker** below.

### Done — the shipRig step-chooser TUI
The source's interactive **step picker** (toggle which steps run before the
release starts) is now built as the **plan editor** above, and goes further with
a **live run dashboard** (streaming per-step status + inline confirm gates) on
top of the same headless pipeline. The `PassthroughChooser` remains the
non-interactive fallback. This was the last real interactive-UI gap.

### Done — the config wizard
The C#-style interactive config walkthrough (net-changesets' `init` and the .NET
`rig setup` config-prompt) is now built as the **`rig init` wizard** above
(ecosystem / solution / default project / exclude / quiet, seeded from
detection), backed by the **primary-ecosystem picker** that resolves an ambiguous
repo on first use and offers to persist the choice. The Go-specific divergences
remain intentional: `changerig init` writes a sensible default (the dropped
interop/sourcePath prompts don't apply to the Go design), and `rig setup` stays
the **shell installer**.

### Remaining (accepted divergences, optional)
- **Interactive `rig default` as its own verb** — minor. The set-default picker
  exists and persists; this only notes that the source exposed a dedicated
  interactive config verb, which here is folded into `default` / `init`.

**Bottom line:** the interactive-UI surface is at or above parity. The two TUI
gaps that remained — the shipRig step-chooser (now the plan editor + live
dashboard) and the C#-style config wizard (now `rig init` + the ecosystem
picker) — are both built; nothing functional is missing.
