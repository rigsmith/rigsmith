# Default commands & source-aware onboarding

**Status:** implemented · **Date:** 2026-06-13 · **Touches:**
`shiprig/internal/cli/root.go`, `changerig/commands/{add,init,status,info}.go`,
`core/config`

Both open questions were resolved **yes**: bare `shiprig` (uninitialized) routes
through the shared setup offer, and `info` now surfaces the active
`versioning.source`.

## Problem

Two things are wrong with how the binaries greet a user.

1. **Both binaries default to `add`.** `changerig/main.go:37-41` and
   `shiprig/internal/cli/root.go:30-34` both wire the bare command to
   `commands.NewAddCmd()`. For `changerig` (capture intent) that's right; for
   `shiprig` (the release front door) it isn't — running `shiprig` with no args
   shouldn't try to *create a changeset*, and in an uninitialized repo it pops a
   "Set up changesets here?" prompt that reads as a non-sequitur for someone who
   just wanted to see release state.

2. **Onboarding only knows the changeset-files world.** The engine supports three
   release sources (`core/config/config.go:44-50`): `changesets` (default),
   `commits` (conventional commits), and `both`. But `init`
   (`changerig/commands/init.go`) is non-interactive and always writes
   changesets-mode config, and the inline `offerInit`
   (`changerig/commands/add.go:204`) scaffolds the same. A user who wants
   commit-driven releases is onboarded into the wrong model, then dead-ended by
   `add` ("versioning.source is \"commits\" — write a conventional commit
   instead", `add.go:55-61`).

## Decisions (confirmed)

1. **Bare `changerig` keeps `add`. Bare `shiprig` becomes `status`** — the
   read-only pending release plan. `status` already handles commit mode
   gracefully (`status.go:95-101`), so it's the right "what would I release?"
   landing for the release front door. `shiprig add` stays available as an
   explicit subcommand.
2. **Onboarding becomes source-aware.** `init` (and the inline setup offer)
   asks how the user drives releases — changeset files / conventional commits /
   both — and writes `versioning.source` into config. A `--source` flag covers
   non-interactive/CI use.
3. Plan doc first (this file), then implement behind a worktree + PR.

## Design

### A. Bare-command defaults

`changerig/main.go` — unchanged (bare = `add`).

`shiprig/internal/cli/root.go` — stop copying `add`'s `RunE`/`Args`/flags onto
the root. Instead build the status command and delegate to it:

```go
status := commands.NewStatusCmd()
root.RunE = status.RunE
root.Args = status.Args
root.Flags().AddFlagSet(status.Flags())
```

`add` remains in the `AddCommand(...)` list so `shiprig add` still works.

**Uninitialized behavior for bare `shiprig`.** Today `status` returns a hard
error in an uninitialized repo (`status.go:100` → `errors.New("no changesets
found")` via the `Open()`/discover path). We don't want bare `shiprig` to throw;
it should orient and offer setup. Two options, pick at implementation time:

- **Preferred:** have `status` detect `!ws.Initialized()` up front and route
  through the same source-aware setup offer used by `add` (see §B), then
  re-evaluate. This keeps one onboarding path for both binaries.
- Minimal: bare `shiprig` checks `ws.Initialized()` before delegating and prints
  a friendly "not set up — run `shiprig init`" hint instead of the raw error.

Going with the preferred path: extract the offer so both `add` and `status`
share it (see §B refactor).

### B. Source-aware onboarding

#### B1. A shared setup offer

`offerInit` (`add.go:204`) currently hard-codes the changesets-mode scaffold.
Refactor it into a reusable `OfferSetup(cmd, ws) (ready bool, err error)` in the
`commands` package that:

1. Off a TTY: returns the existing actionable error pointing at `init`
   (`add.go:206-208`) — but generalized to mention `--source` so CI users learn
   the knob exists.
2. On a TTY: shows where config will live, then runs a **source picker** (a `huh`
   `Select`) — *Changeset files* / *Conventional commits* / *Both* — followed by
   the existing confirm, and scaffolds with the chosen source.

Both `add` (uninitialized → before creating a changeset) and `status`
(uninitialized → before computing a plan) call `OfferSetup`. If the user picks
**commits**, `add` then naturally hits its existing commit-mode guidance
(`add.go:55-61`) instead of writing a file — which is now *correct* rather than a
dead-end, because the user explicitly chose commits.

#### B2. `init` gets a `--source` flag and an interactive picker

`changerig/commands/init.go`:

- Add `--source changesets|commits|both` (default `changesets`).
- When `--source` is **not** passed **and** stdin/stdout are a TTY, run the same
  source picker as the inline offer.
- Pass the resolved source into `Scaffold`.

`shiprig` already exposes `commands.NewInitCmd()`
(`root.go:37`), so `shiprig init --source commits` works for free.

#### B3. `Scaffold` writes the chosen source

`Scaffold(ws)` (`init.go:56`) becomes `Scaffold(ws, source)`. The default config
JSON (`init.go:11-20`) stays as-is for `changesets` (omit the block — empty
source already normalizes to changesets, `config.go:67-75`). For `commits` /
`both`, inject:

```json
"versioning": { "source": "commits" }
```

Note the config still lives at `.changeset/config.json` even in commits mode —
`.changeset/` is the config home, not just a changeset-file folder. The README
copy (`init.go:22-26`) should branch: in commits mode it explains "releases come
from conventional commits; no changeset files needed," with a pointer to `add`
for the occasional explicit changeset only in `both` mode.

#### B4. Callers to update

`Scaffold` has two call sites — `NewInitCmd` (`init.go:38`) and `offerInit`
(`add.go:225`). Both move to the new signature. `setup_test.go` /
`changerig/cmdtest` reference these; update accordingly.

## File-by-file change list

| File | Change |
| --- | --- |
| `shiprig/internal/cli/root.go` | Bare command delegates to `status`, not `add`. |
| `changerig/commands/add.go` | Extract `offerInit` → shared `OfferSetup`; keep `add` calling it. |
| `changerig/commands/status.go` | Uninitialized → call `OfferSetup` before erroring. |
| `changerig/commands/init.go` | `--source` flag + interactive picker; `Scaffold(ws, source)`; source-aware README + config JSON. |
| `core/config` | No code change needed (struct already supports it); confirm round-trip of an injected `versioning.source`. |
| tests | `setup_test.go`, `cmdtest` helpers updated for new `Scaffold` signature + new behaviors. |

## Test plan

- **Unit:** `Scaffold` writes `versioning.source` correctly for each of the three
  choices; `changesets` omits the block and round-trips to `SourceChangesets`.
- **Bare `shiprig` (initialized):** prints the status plan, no `add` side effects.
- **Bare `shiprig` (uninitialized, TTY):** runs the source picker, scaffolds,
  then shows status — no raw "no changesets found" error.
- **Bare `shiprig` (uninitialized, non-TTY):** actionable error mentioning
  `init`/`--source`, non-zero exit.
- **`init --source commits` (non-TTY):** writes commits-mode config without
  prompting.
- **Bare `changerig` after choosing commits:** lands on the commit-mode guidance
  (`add.go:55-61`), creates no file.
- **`shiprig add` still works** as an explicit subcommand.

## Open questions

1. **Banner/short text.** `shiprig`'s `Short` ("Uniform changeset → version →
   publish…") is fine, but should bare `shiprig` print the banner before the
   status plan? `status` today prints no banner; fang prints it on `--help`.
   Likely leave as-is.
2. **`both` README wording** — how much to say about when to still write an
   explicit changeset. Keep it to one line.
3. Should `info` (`info.go`) surface the active `versioning.source`? Cheap, and
   helps users confirm onboarding took. Suggest yes as a small add.
