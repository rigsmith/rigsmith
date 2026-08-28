# clauderig UI — plan

*Drafted 2026-08-07 from the Air/Pro divergence incident (65 unmerged commits sat invisible
behind a failed fast-forward; resolved by hand). Decisions locked with John: menu-bar-first
app + window, CLI stays the engine, lives in this repo under `ui/`.*

*Revised 2026-08-08: stack changed from Avalonia to **Wails v3**; engine seam changed from
pure shell-out to **hybrid** (import for reads, shell out for writes). Rationale in
[Stack](#stack). Everything in [Why](#why-what-the-incident-proved) is unchanged — the
incident analysis drove the feature set, not the toolkit.*

## Shape

A Wails v3 app with two faces:

- **Tray icon** — the ambient face. Green (synced) / amber (behind remote) / red (diverged
  or last sync failed). Menu: sync now, per-device freshness, open window.
- **Window** — the interactive face: device board, activity feed, conflict resolution,
  remote-session browsing, account switching. Hidden at startup; the tray is the app.

## Stack

**Wails v3** (`github.com/wailsapp/wails/v3`), pinned to a specific beta. Go backend,
platform-native webview frontend (WKWebView / WebView2 / WebKitGTK).

Chosen over Avalonia and Tauri because:

1. **Same language and module as the engine.** `ui/` sits inside
   `github.com/rigsmith/rigsmith`, so it can import `internal/clauderig/...` directly —
   see [Engine seam](#engine-seam). Avalonia and Tauri are both permanently stuck on
   subprocess + JSON for every read.
2. **No new toolchain.** Go is already the build, test, and release language for all four
   CLIs. Avalonia adds .NET + Velopack-for-ourselves; Tauri adds Rust *and* keeps the Go
   subprocess boundary anyway — a third language buying nothing.
3. **The design system is already HTML.** `design/` (brand.js, cli.js, docs.js) and the
   VitePress site define the visual language. A webview frontend inherits it; XAML would
   mean re-authoring it.
4. **Weight.** ~15 MB order-of-magnitude vs a self-contained .NET bundle. Not the deciding
   factor, but it was the objection that reopened the question.

Wails v3 went **beta on 2026-08-02** (latest `v3.0.0-beta.5`, 2026-08-07) after a long
alpha. The desktop API is declared stable and in production use; mobile is explicitly
outside the compatibility promise and we don't want it. v2 is not an option — it has no
built-in systray at all. See [Risks](#risks-and-open-spikes) for what this costs us.

Frontend framework is deliberately unpinned here. The window is four screens of lists and
detail panes; pick at scaffold time, keep it boring, reuse the brand tokens.

## Engine seam

**Import for reads. Shell out for writes.**

The UI is not a second implementation of sync. But it also shouldn't spawn a subprocess
every 30 seconds to learn something it can compute in-process from the same code the CLI
uses.

**Reads — direct import.** `ui/` is in the same module, so `internal/` is importable:

| Need | Package | Entry point |
|---|---|---|
| Status poll, root state | `internal/clauderig/status` | `Gather(ctx, cfg, me, staging, settingsPath) Info` |
| Device board | `internal/clauderig/devices` | `Load(dir) (*Registry, error)` |
| Session lists, transcripts | `internal/clauderig/session` | `Build(roots) Index`, `FirstPrompt(path)` |
| Search | `internal/clauderig/search` | package API |
| Config, machine identity | `internal/clauderig/config` | package API |

These are pure functions over plain structs with no CLI coupling — exactly the shape the
UI wants. Polling becomes a function call, transcript rendering reads the same `session.Meta`
the CLI does, and there is no JSON round-trip or struct duplication to keep in sync.

**Writes — shell out to the `clauderig` binary.** `sync`, `pull`, `restore`, `merge`,
`materialize`, `account switch`, `device rm`. These are the operations with side effects,
safety guards, and streamed progress the drawer wants to render. Keeping them behind the
binary means the CLI stays the single implementation of anything that can lose data, and
the UI can't drift from it.

**Binary resolution:** prefer a `clauderig` sitting next to the UI executable, fall back to
`PATH`, surface a clear error if neither resolves. Never assume an install location.

**Consequence for Phase 0:** `--json` is no longer on the UI's critical path. It's still
worth building — for scripting, for Tweed, and to keep the seam honest — but the UI can
start before it lands. What the UI *does* need from Phase 0 is the **divergence fields on
`status.Info` itself** (it currently carries `LastSync`, `Dirty`, `Roots`, `Hooks`,
`Devices` — no ahead/behind, no would-this-conflict, no per-root sync outcome). Build those
into the struct; `--json` becomes a thin marshal of the same thing.

## Why (what the incident proved)

1. **Divergence is invisible.** `clauderig-devices.json` records *last push*, not health.
   This Air showed "synced 5 minutes ago" while 65 commits behind all day. The registry
   can't express ahead/behind, and the failed pull surfaced only in a SessionStart hook
   message nobody reads.
2. **Sync failures die in hook stderr.** A Jul-27 qbo session shows the Stop-hook sync
   refusing to run for days-worth of turns ("Secret tripwire: 12 value(s) look like
   credentials"). No surface ever showed it.
3. **Conflict resolution has correct answers a tool can encode.** Every conflict in the
   hand-merge fell into a policy: timestamps → newest wins; `MEMORY.md` → union keyed by
   memory filename; transcripts → superset (append-only line union); manifests → union of
   entries. None needed judgment — they needed a button. Full spec:
   [CLAUDERIG-MERGE-POLICIES.md](CLAUDERIG-MERGE-POLICIES.md).
4. **Reading another machine's sessions shouldn't require merging.** The Pro's chat was
   readable via `git show origin/main:<path>` the whole time. That's a viewer feature.
5. **Ghost devices** — the `this` entry (removed 08-07) came from pre-hostname-detection
   registration; there's no `device rm` and no validation.

## Layout

```
ui/
├── Taskfile.yml           # wails3 build entry; includes build/*/Taskfile.yml
├── main.go                # app + systray + window wiring
├── bridge/                # service structs bound to the frontend
│   ├── status.go          #   imports internal/clauderig/status, devices
│   ├── sessions.go        #   imports internal/clauderig/session, search
│   └── actions.go         #   shells out; streams stdout/stderr to the drawer
├── assets/                # tray icons: 3 states x {template, dark, light}
├── frontend/
│   ├── src/               # reuses design/ brand tokens
│   └── dist/              # go:embed all:frontend/dist
└── build/                 # wails per-OS Taskfiles, Info.plist, icons, nsis
```

**`health` moved down, 2026-08-08.** It was drafted as `ui/health/` and now lives at
`internal/clauderig/health`, because the CLI wants it too: `status --json` emits the same
`level`/`reason` the tray paints with. Anything both front ends need belongs below both —
the same reason `journal.Record.Summary` moved out of `ui/bridge`. A CLI that depends on
the UI package would have been backwards.

## Tray specifics

Verified against the v3 systray API (`application.SystemTray`):

- **Runtime colour swap** is `SetIcon([]byte)`, callable after creation — that's the
  green/amber/red mechanism. Each state is a separate embedded asset, not a tint.
- **macOS** gets `SetTemplateIcon([]byte)`: black + transparent only, auto-adapts to
  light/dark menu bars. Prefer it over `SetDarkModeIcon()`. Caveat: a template icon is
  monochrome *by definition*, so health colour can't ride on the glyph itself on macOS —
  either use a non-template coloured icon and handle dark mode manually via
  `SetDarkModeIcon()`, or keep the glyph template and carry state in a badge/label.
  **Decide this deliberately during Phase 1**; it's the one place the three-state design
  collides with platform convention.
- **Window attach** is `AttachWindow(win)` + `WindowOffset()` + `WindowDebounce()` for
  click-to-toggle, plus `OnClick`/`OnRightClick` handlers if we want custom behaviour.
- **Window lifecycle:** start hidden via `WebviewWindowOptions{Hidden: true}`; hide instead
  of quit on close via a cancellable pre-close hook —
  `RegisterHook(events.Common.WindowClosing, func(e){ e.Cancel(); win.Hide() })`.
  `OnWindowEvent` fires too late to prevent the close.
- **Windows** wants 16x16 or 32x32 PNG/ICO; tooltips cap at 127 UTF-16 chars.
- **Linux is desktop-environment dependent** — GNOME needs an AppIndicator shell extension
  for the tray to appear at all; KDE/XFCE are fine. This is an OS-level reality, not a
  Wails limitation, and it would be identical under Avalonia or Tauri. Plan for a
  "tray didn't appear" fallback: a `--window` flag that opens the window directly.

## Build & release

The existing pipeline builds **bare CLI binaries** — four `builds` entries, `CGO_ENABLED=0`,
archived as tar.gz/zip, with `notarize.macos` signing the raw binaries via GoReleaser's
bundled quill on an **ubuntu-latest** runner (deliberately: no macOS runner needed today).
A GUI app does not fit that shape, and this is the single biggest piece of new work.

What breaks:

- **Wails needs CGO on macOS and Linux** (WKWebView, GTK/WebKitGTK). The whole existing
  matrix is `CGO_ENABLED=0`. This build gets its own settings and can't cross-compile as
  freely — Linux and macOS targets need their own runners or Docker.
- **macOS builds need an explicit deployment target** (confirmed 2026-08-08). Wails' ObjC
  declares `-mmacosx-version-min=10.13`, which a current SDK clamps up to its own floor,
  while Go's linker stamps 11.0 — so a bare `go build ./ui` emits a screenful of
  `ld: warning: object file … built for newer 'macOS' version`. Setting both
  `MACOSX_DEPLOYMENT_TARGET=12.0` and `CGO_LDFLAGS=-mmacosx-version-min=12.0` aligns them
  (12.0 is what `wails3 init` generates). Whatever ships the UI — Taskfile, GoReleaser
  entry, or our own packer — has to carry both. Detail in
  [`ui/README.md`](../ui/README.md).
- **A `.app` bundle is not a binary.** `notarize.macos` targets build *ids* and signs
  Mach-O binaries; it has no notion of a bundle. GoReleaser OSS has no `app_bundles`
  (that's Pro). So the current notarization path cannot sign the UI.
- **Wails has its own packaging** (`wails3 package`): `.app` + DMG on macOS, NSIS/MSIX on
  Windows, AppImage/deb/rpm on Linux, with `wails3 task darwin:sign:notarize` for
  notarization — but that path wants a macOS runner.

**Preferred approach: dogfood our own packer.** `core/ecosystem/velopack/` already builds
macOS `.app` bundles, renders a templated `Info.plist`, wraps a notarized `.app` in a DMG,
and `core/dsstore/` generates the drag-to-install `.DS_Store` headlessly with no Finder.
`core/sign/` already resolves signing secrets for Tauri/Electron builds. Today all of that
packages *users'* desktop apps and is never run against rigsmith itself. The clauderig UI
would be the first time we ship what we sell — which is both the cheapest path and the best
possible test of that code. Velopack also brings auto-update, which a tray app that must
stay current genuinely wants.

Fallback if that proves awkward: add a macOS runner to the release workflow and use
`wails3 task darwin:sign:notarize` directly. Decide after a spike; don't design for both.

**Enumeration points that need a new entry when the UI becomes a released artifact:**
`.goreleaser.yaml` (`builds`, `archives`, `notarize.ids`, `homebrew_casks`, `winget`, and
the `rigsmith` bundle archive + cask `binaries:` list), `scripts/winres.sh` (hardcoded
`for tool in …` loop), `build/winres/<tool>.json` + `build/icons/<tool>.png`,
`scripts/npm/build-packages.mjs` (`TOOLS` map), and `scripts/install.sh` /
`scripts/install.ps1` accepted tool names. `scripts/dev-install` and
`scripts/source-install` auto-discover from `cmd/` and need no change — though the UI lives
at `ui/`, not `cmd/`, so confirm what they do with it.

Note also: CI (`ci.yml`) is Go-only today — no Node step. A webview frontend adds one.

## Phase 0 — CLI groundwork (Go, no UI yet)

Unchanged in substance; `--json` is no longer blocking (see [Engine seam](#engine-seam)).

- **Divergence fields on `status.Info`**: ahead/behind vs origin, whether a merge would
  conflict, last sync outcome per root. The UI reads the struct; `--json` marshals it.
- ~~`--json` on `status` (and `account list`, `search`) for scripting and Tweed.~~
  **Done 2026-08-08.** `status --json` embeds `status.Info` verbatim and adds the health
  verdict (`level`/`reason`/`summary`/`action`) plus the last journal record — the two
  things a caller would otherwise re-derive. It skips the reachability probe, so a hung
  remote can't hang a poller. `search --json` and `account list --json` likewise; the
  account document carries `desynced` as a field so a script can't miss the warning the
  styled output prints in prose.
- **Sync journal**: every sync/pull/restore appends a JSONL record (when, machine, files
  written, redactions, aged-out, LEAK refusals, error). The activity feed reads this;
  it also makes hook-failures durable instead of stderr-only.
- ~~`clauderig merge`: encode the resolution policies above.~~ **Done 2026-08-08.**
  `internal/clauderig/merge` holds five pure policies (devices-union, manifest-union,
  memory-union, transcript-superset, newest-timestamp); the command fetches, merges
  `--no-ff`, applies them per conflicted file and prints a ledger of what each did.
  Residual conflicts exit nonzero, named, with the repo left mid-merge for the user's
  own mergetool (`--abort` backs it out). This is the engine behind the UI's one-click
  **Resolve**.
- ~~`clauderig peek <device> [list|show <session>|materialize <session>]`~~ **Done
  2026-08-08**, as `peek list|show|materialize` with `--device` as a filter rather than a
  positional — sessions aren't partitioned by machine in the repo, so attribution is
  derived from each file's most recent `clauderig sync: <machine>` commit. `list` shows
  titles (first prompt, read from the blob), `show` renders the conversation (`--raw` for
  JSONL), `materialize` copies one in additively and refuses if the id already exists
  locally. Slugs are rewritten for this machine via the manifest, as restore does.
- ~~`clauderig device rm <name>` + reject registration when hostname resolution fails
  (the `this` glitch).~~ **Done 2026-08-08.** `clauderig device list` / `remove` (alias
  `rm`, interactive-confirm only), and `sync` now declines to register a machine whose
  name falls through to `config.UnresolvedName`. Details in
  [CLAUDERIG-MERGE-POLICIES.md](CLAUDERIG-MERGE-POLICIES.md#registry-hygiene).
- ~~Restore safety: `restore` copies unconditionally with no live-session guard.~~
  **Done 2026-08-08.** `engine/restore.go` skips the transcript of every running
  session (`projects/<slug>/<sessionId>.jsonl`, via `account.RunningInstances`) and
  names what it kept. Per-session rather than per-project, so unrelated sessions still
  restore. Details in [CLAUDERIG-MERGE-POLICIES.md](CLAUDERIG-MERGE-POLICIES.md).

## Phase 1 — Tray + status window

- **Spike first** (before any UI code): confirm a Wails v3 entrypoint can live at `ui/`
  inside this module rather than at a project root. See [Risks](#risks-and-open-spikes).
- SystemTray with the three-state health colour, driven by `health.From(status.Gather(...))`
  in-process (30–60s; immediate refresh after any action).
- Device board: one card per device — last sync, ahead/behind, OS, Claude version,
  staleness colouring. (`devices.Registry` + journal.)
- Activity feed: recent syncs across machines with outcomes; failures and secret-tripwire
  refusals rendered as first-class rows, not buried.
- ~~"Sync now" / "Pull" actions shelling out, with streamed CLI output in a drawer.~~
  **Done 2026-08-08.** `ui/bridge` gained `runner.go` (allowlisted verbs → fixed argv,
  one action at a time, line-streamed, ANSI-stripped) and `actions.go` (the bound
  service, emitting `clauderig:action:{start,line,done}`). Buttons follow the health
  reason, so the banner's advice and the button you get are the same thing; the tray
  carries Sync now / Pull too, and running one from there opens the window so a
  tripwire refusal can't happen unwatched. **Resolve (merge)** is offered on a diverged
  state — slightly ahead of the phase line, but `merge` exists and a red banner with no
  action was worse; Phase 2's work is the structured per-file ledger panel, not the
  button. Binary resolution prefers a `clauderig` beside the app over PATH, so a
  packaged bundle drives the CLI it shipped with.

## Phase 2 — Resolve + browse

- **Resolve button — done 2026-08-08.** The diverged banner offers it, and `merge`
  grew `--json` so the ledger is data rather than scraped text (`applyPolicies` now
  returns `[]Resolution`; the styled output and `--json` render one set of facts).
- **Residual-conflict two-sided picker — NOT built.** Deliberately deferred: it is a
  data-editing surface, and the only cases reaching it are files no policy understands,
  where picking a side wholesale is exactly the hazard
  [CLAUDERIG-MERGE-POLICIES.md](CLAUDERIG-MERGE-POLICIES.md) warns about. Today those
  files stay conflicted with git's markers intact and the merge exits nonzero naming
  them, so `git mergetool` handles them safely. Building a picker needs its own design
  pass on what "pick a side" means per file class.
- **Remote session browser — done 2026-08-08.** A Sessions tab over `ui/bridge/sessions.go`:
  machine filter, titles, read-only transcript rendering (bounded at 400 turns and
  *says so* when it clips), and **Bring to this Mac** → `peek materialize`, greyed out
  for sessions already here.
- **Search — partial.** The browser filters the listed sessions by title client-side.
  Full-text search across live + synced (`search` package in-process, `search --json`
  shape) is not wired into the window yet; the CLI has it.

## Phase 3 — Accounts

- **Done 2026-08-08**, with one gap. An Accounts tab over `ui/bridge/accounts.go`:
  logins list with the live one marked, capture (`account add`), and switch.
- **The switch guard is surfaced, not routed around.** `Get` reports every running
  Claude Code process, and the switch buttons are disabled while any exist, naming the
  pids — a swap underneath a live session corrupts its identity, so the UI shows why it
  can't rather than letting the CLI's refusal look like a bug. The both-halves swap
  itself stays in the CLI; the UI shells out to it.
- **Desync detector — surfaced, no resync button.** `Diagnose().InSync` drives a warning
  banner. The one-click "resync" action isn't built; it points at
  `clauderig account doctor` instead, which is the tool that actually repairs it.
- **Remove — not built.** `account remove` is interactive-confirm-only by design
  (destructive commands here refuse without a terminal), so wiring it needs a
  confirmation surface in the window first.

## Risks and open spikes

Ordered by how much they'd hurt to discover late.

1. ~~**Entrypoint location — unverified.**~~ **Resolved 2026-08-08.** `ui/` hosts the
   entrypoint with no special handling: `go build ./ui` produces a working binary that
   imports `internal/clauderig/status` directly and reads the live device registry. Go
   does not care where a `main` package lives, and `application.New` has no opinion about
   the project root. No nested module, so the direct-import seam stands as designed.

   Still unverified: the **`wails3` CLI tooling** (`wails3 build` / `dev` / `package`)
   against a non-root entrypoint. That only matters for packaging, not development —
   `go build ./ui` and `go run ./ui` work today — so it folds into the build-and-release
   work in item 4 rather than blocking Phase 1.
2. **Beta churn.** Six days into beta after a multi-year alpha. Pin the exact version, don't
   float, and expect to read changelogs on upgrade. The v2→v3 migration guide is explicit
   that v3 is a port rather than a version bump — so there is no cheap retreat to v2, and v2
   has no systray anyway.
3. **macOS template icon vs three-state colour** — see [Tray specifics](#tray-specifics).
   **Answered 2026-08-08, provisionally: colour wins.** The icons are non-template, with
   `SetDarkModeIcon` handling light/dark by hand. Ambient health signalling is the tray's
   only job, and a template icon is monochrome by definition, so it cannot carry the
   state. The cost is that the icon won't tint with macOS accent settings. Rationale and
   the alternative live in [`ui/assets/README.md`](../ui/assets/README.md); revisit once
   it has been in a real menu bar for a while.
4. **CGO + the release pipeline.** The largest chunk of unbudgeted work; see
   [Build & release](#build--release).
5. **Linux runtime deps are self-contradictory in the docs** — the packaging page names
   GTK4/WebKitGTK 6.0 as the default while listing `libwebkit2gtk-4.1-0` as the runtime
   dependency. The GTK3 path (`-tags gtk3`) is deprecated for removal in v3.1, so target
   GTK4 and verify the generated deb/rpm `depends:` by hand before shipping Linux packages.

## Open

- ~~**Name.**~~ **Settled 2026-08-08: `claudeRig UI`.** Lowercase "c" to match the
  wordmark the repo and `design/marks.js` already use everywhere (38 occurrences of
  `claudeRig`, none of `ClaudeRig`) — the app shouldn't introduce a second spelling of
  its own product. Bundle identifier `dev.rigsmith.clauderig-ui`, following the
  `dev.rigsmith.<thing>` convention in the packaging examples. Both are `AppName` /
  `BundleID` in `ui/main.go`, so packaging reads them rather than re-deciding. Binary
  name `clauderig-ui`, matching the all-lowercase CLI executables.
- Frontend framework choice. **Deferred again at scaffold time**: the status window is
  plain HTML/CSS/JS on the `design/` tokens, no framework and no bundler, which keeps CI
  Go-only (see [Build & release](#build--release)). Revisit when the window grows past the
  status screen — the remote-session browser in Phase 2 is the likely trigger. Note the
  frontend calls bound methods by their full Go FQN string because we skip
  `wails3 generate bindings`; `ui/bridge/binding_test.go` fails if that drifts.
- Whether Tweed later embeds the same status readout — it still can, over `--json`, and now
  also over the `health` package if it ever moves in-process.
- Auto-resolve-on-pull: once `clauderig merge` is trusted, the SessionStart hook could
  invoke it instead of failing the fast-forward. Decide after the button has mileage.
