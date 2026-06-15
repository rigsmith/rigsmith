# doctor: install missing tools on request

> Status: **designing** (2026-06-14). Lets each rig's `doctor` command actually
> install the tools it reports as missing, at the user's request — on one shared
> model so all four CLIs behave identically.

## The job

Today `doctor` *reports* missing tools and prints how to get them; the user then
copies the command and runs it themselves. Both halves of "just install it for
me" already exist in the tree — they're simply not wired to the doctor surface:

- **`clauderig doctor`** already carries a per-check `Fix func(context.Context) error`
  + `FixLabel`, renders a **pre-checked multi-select** of fixable issues, and has
  `--fix` for non-interactive apply (`internal/clauderig/{doctor,commands}`). But
  its fixes only repair clauderig's *own* wiring (hooks, gitignore, CLAUDE.md
  blocks); its checks for actual binaries (`git`, `gh`, `rig`/`clauderig` on PATH)
  only print a `Hint`.
- **`rig doctor`** has the opposite half: the `extTool` engine
  (`internal/rig/cli/exttool.go`) models the full `detect → prompt → install →
  persist` lifecycle (`install []string`, `installable()`, `runInstall()`, a
  yes/not-now/never `prompt()`, and `auto|off|install` config modes) — but it only
  fires *lazily on-use* (e.g. when `rig coverage` needs `cargo-llvm-cov`). From
  `doctor`, the install command is rendered as **text** (`toolHowto`), never run.

`changerig` and `shiprig` have no `doctor` at all yet.

The plan lifts the **model** into `core/`, the **presentation** into one shared
internal package, and connects rig's installers to it.

## Scope decisions

- **Owned tools only.** A `Fix` is attached only when the tool has an exact
  install command rig owns — the `extTool`s: `cargo-llvm-cov`, `cargo-outdated`,
  `cargo-watch`, `wgo`, ReportGenerator (fetch-on-use). The **system toolchains**
  (`go`, `node`, `dotnet`, `cargo`) and unowned binaries (`git`, `gh`) stay
  **report-only** with their current hint / download link — no package-manager
  (brew/apt/winget/rustup) guessing. They naturally fall through: no installer ⇒
  no `Fix` ⇒ report-only.
- **Shared `core/doctor` model.** One contract, adopted by all four CLIs so the
  install UX is written once and identical everywhere.

## Three layers

### 1. `core/doctor` (new) — the model contract, stdlib-only

`core/` is zero-external-dependency by rule, and clauderig's doctor model already
does no terminal I/O, so it drops in unchanged:

```go
type Status int // OK, Warn, Fail, Info

type Result struct {
    Name     string
    Status   Status
    Detail   string
    Hint     string                      // manual remediation when Fix is nil
    Fix      func(context.Context) error // nil ⇒ not auto-fixable (report-only)
    FixLabel string
}

type Section struct {
    Title   string
    Results []Result
}

func Counts(sections []Section) (fails, warns, fixable int)
func Fixable(sections []Section) []Result
```

The *capability to install* lives inside the `Fix` closure, so `core` carries
installers while staying dependency-free.

### 2. `internal/doctorui` (new) — shared presentation + apply loop

The huh / lipgloss / cobra parts can't live in `core/`, so they become one shared
internal package, generalized from clauderig's `commands/doctor.go`:

- `Render(out, sections)` — sectioned ✓/!/✗ report with hints.
- `RunFixes(cmd, sections, fixAll) (exitNonZero bool)` — the pre-checked
  multi-select ("all selected — space toggles, enter applies"), the apply loop,
  and the fails-remaining tally.
- honors `--fix` (apply all, non-interactive) and a non-TTY (print "run with
  `--fix`", apply nothing).

All four CLIs call this, so "install at the user's request" is one implementation.

### 3. Per-tool check packages

Each tool builds `[]doctor.Section` and hands it to `doctorui`:

- **clauderig** — keeps its current checks/fixes; only its *types* move to
  `core/doctor`. Done **first**, as the proof the shared layer preserves today's
  tested behavior before rig adopts it.
- **rig** — `toolChecks` wraps each *owned* missing `extTool` in a `Fix` that
  calls the existing `runInstall`; attaches it only when `installable(root)`.
  Toolchains stay report-only. rig's live spinner checklist (`doctorlive.go`)
  stays rig-local; the fix step runs *after* it resolves, so they compose:
  live checklist → collect results → `doctorui.RunFixes`.
- **changerig / shiprig** — get a `doctor` for free later by building sections
  and calling `doctorui`.

## Wrinkles

- **Severity mapping.** rig's doctor uses `docOK/docWarn/docError`; core uses
  `OK/Warn/Fail/Info`. 1:1 map (`docError→Fail`). rig's `check`/`pendingCheck`
  and live renderer reference the rig-local type, so the bulk of rig's diff is a
  mechanical adapt, not a rewrite.
- **Config modes / "never ask again".** rig's `auto|off|install` modes and the
  never-ask persistence still apply: a tool set `off` reports report-only in
  doctor too (no `Fix`), and accepting an install from doctor persists `install`
  the same way the on-use path does — doctor is just another entry point to the
  same lifecycle.
- **Idempotent re-run.** After applying fixes, the fixed checks should pass on the
  next `doctor` run; installers that fail surface inline (`✗ <label>: <err>`) and
  leave the exit code non-zero.

## Build order

1. `core/doctor` — move the model out of `internal/clauderig/doctor`, leave the
   checks behind.
2. `internal/doctorui` — extract render + fix-selection from clauderig's
   `commands/doctor.go`; clauderig delegates to it (behavior-preserving).
3. `rig doctor` — adapt to `core/doctor` types; attach `Fix` to owned `extTool`
   misses; run `doctorui.RunFixes` after the live checklist.
4. (later) `changerig` / `shiprig` doctors on the same model.
