# claudeRig UI

The ambient face of clauderig: a menu bar icon that colours itself from real
sync state, plus a window for the detail. Design and phasing live in
[docs/CLAUDERIG-UI-PLAN.md](../docs/CLAUDERIG-UI-PLAN.md).

Wails v3 (`v3.0.0-beta.15`, pinned), Go backend, platform webview frontend.

| | |
|---|---|
| Display name | `claudeRig UI` — lowercase "c", matching the `claudeRig` wordmark |
| Bundle identifier | `dev.rigsmith.clauderig-ui` |
| Binary | `clauderig-ui` |

Name and bundle id are `AppName` / `BundleID` in `main.go`. Packaging can't read a Go
constant, so they're duplicated here deliberately — change both together.

## Build and run

```sh
MACOSX_DEPLOYMENT_TARGET=12.0 \
CGO_ENABLED=1 \
CGO_LDFLAGS="-O2 -g -mmacosx-version-min=12.0" \
  go run ./ui --window
```

`--window` opens the status window at startup, `--sessions` the sessions
manager, and `--notice` the Claude Desktop notice — which is otherwise raised
only by a launch you cannot schedule, so it is the way to look at that window on
purpose. Without any of them the app starts in the tray only — which is the intended
behaviour, and also the escape hatch for Linux desktops where the tray never
appears (GNOME needs an AppIndicator extension).

`CLAUDERIG_TERMINAL` names the application the sessions window's **Open in
terminal** button hands the resume script to; it defaults to `Terminal`, which
is the one macOS always has. The **Copy command** button beside it is the path
that works with any terminal, multiplexer or remote host.

All three flags reveal their window on `events.Common.ApplicationStarted` rather
than before `app.Run()`. Showing a window before the app is running silently does
nothing for any window but the first, which made `--sessions` look like a dead
flag while the same window opened fine from the tray menu.

### Why the macOS deployment-target flags

Bare `go build ./ui` works, but prints a screen of

```
ld: warning: object file (…) was built for newer 'macOS' version (26.0) than being linked (11.0)
```

Three versions have to agree and by default none of them do:

| | Default | Set by |
|---|---|---|
| cgo objects | the SDK's version (26.0) | `MACOSX_DEPLOYMENT_TARGET` |
| the link | 11.0 (Go's minimum) | `CGO_LDFLAGS=-mmacosx-version-min=` |
| Wails' own ObjC | declares 10.13, **clamped up** to the SDK floor | not settable |

Because the SDK clamps Wails' declared 10.13 upward, the objects cannot be
lowered to Go's 11.0 — the link has to be raised instead. Setting both variables
to 12.0 makes all three agree and the warnings go to zero (`otool -l` then shows
`minos 12.0`).

12.0 is Wails' own number: it is what `wails3 init` writes into the darwin
Taskfile it generates, and what their test suite asserts.

Neither flag is needed for the four CLIs — they build `CGO_ENABLED=0` and are
unaffected.

**Cache note:** `MACOSX_DEPLOYMENT_TARGET` does not invalidate every cached cgo
object, so a tree built once without it can keep emitting warnings until
`go clean -cache`. Set both variables consistently and it stays quiet.

## Layout

| Path | What |
|---|---|
| `main.go` | app, tray, window wiring, the poll loop |
| `health/` | `status.Info` → green/amber/red, in one place |
| `bridge/` | services bound to the frontend; the read half of the engine seam |
| `bridge/sessions.go` | the REMOTE session browser — `peek` over the staging repo |
| `bridge/library.go` | the sessions manager — every session this machine can see |
| `assets/` | tray icons, three states × light/dark ([README](assets/README.md)) |
| `frontend/dist/index.html` | the tray popup — status and a compact sessions view |
| `frontend/dist/sessions.html` | the full sessions workspace |

## The engine seam

**Import for reads, shell out for writes.** `ui/` is in the same module, so
`bridge` calls `internal/clauderig/...` in-process — no subprocess or JSON
round-trip to learn the status. Anything with a side effect (`sync`, `pull`,
`restore`, `merge`, `account switch`) shells out to the `clauderig` binary
instead, so the CLI stays the single implementation of everything that can lose
data.

The frontend calls bound methods by their full Go FQN
(`github.com/rigsmith/rigsmith/ui/bridge.Status.Get`) because we deliberately
skip `wails3 generate bindings` — it would add a Node step to a Go-only CI.
`bridge/binding_test.go` fails if the frontend and the Go signature drift apart,
which would otherwise compile clean and break only at runtime. It scans every
`frontend/dist/*.html`, so a method wired from the sessions window counts the
same as one wired from the status page.

`Library` is the sessions surface: it answers "what sessions do I have",
merging the live `~/.claude`, every Desktop install and the synced copy into one
row each, via `internal/clauderig/sessions`. A `Sessions` service used to sit
beside it reading the remote through `peek`; it was folded in and removed once
the manager covered listing, reading, and — via *Bring to this Mac* —
materialising.

## The Claude Desktop window list

The tray's **Claude Desktop** submenu names every open Claude Desktop window —
each profile, the machine-wide app, and anything running on a directory outside
the store — and clicking one brings that window forward.

This exists because the Dock cannot. Each instance does get its own tile, but
they carry the same icon and the same name, so the only way to tell the work
profile from the machine-wide app is to click one and look. Nothing in macOS
badges another application's tile — the tile belongs to that process, and there
is no API into it — so the discrimination has to live somewhere we own.

Raising is by **pid**, through `desktop.App.Raise`. Activating the application
is what the Dock already does and is exactly the ambiguity being solved: every
instance is one application to the OS, which then picks the window itself. On
macOS that means System Events and therefore Automation permission, so the first
raise prompts; on Windows it is not supported and the CLI says so rather than
pretending.

Two details that are deliberate rather than incidental:

- **The menu is rebuilt only when the set of windows changes**, keyed on the
  pids and their labels. A native menu rewritten on every ten-second tick is a
  menu that can be rewritten under a hand already reaching for it.
- **The pid is re-validated before the raise.** It comes from a menu built up to
  ten seconds ago; a window that has closed should raise nothing, and a pid the
  OS has recycled belongs to another program by now.

**Open the main app** at the bottom runs `clauderig desktop main`, which decides
for itself whether to launch or raise — so the item works whether or not that
window exists, and this menu never has to have guessed right.

## The Claude Desktop notice

A watch (`watchDesktop` in `main.go`) scans for running Claude Desktop windows
every ten seconds — five while the notice is on screen — and raises a small
window when the **machine-wide** install is launched: the one started with no
`--user-data-dir`, which is what you get from the Dock, Spotlight or the Start
menu. While it is open, a `claude://` deep link is routed by scheme rather than
to a particular window, so **Open in Desktop** can land there instead of the
profile that was picked, and `clauderig desktop send --session` refuses rather
than guess.

It is our own window rather than an OS notification on purpose: a real
notification needs a signed `.app` bundle and the user's permission, so it would
be silent in a dev build and silent for anyone who ever declined the prompt.

The decisions about *when* to raise it are in `bridge.DesktopAlarm`, which is
where the tests are — a window already open when the tray starts is not a
launch, a failed process scan holds the previous state rather than reading as
"closed", and a machine with no clauderig profiles is never warned at all.

**Don't warn again** on the notice writes `desktopWarnOnLaunch: false` to
`~/.clauderig/ui-state.json` — the UI's own machine-local settings, deliberately
not a `clauderig` config key. The tray's **Warn when Claude Desktop opens**
checkbox is the way back on.

## Releasing

The window ships on its **own tag**, `ui/vX.Y.Z`, not on the CLIs' `vX.Y.Z`. It
is a separate module at 0.x while they are at 1.x, and it no longer has to wait
for a toolchain release to reach anyone.

1. `changerig add --scope clauderig-ui` as usual; `shiprig version` bumps the
   `// rigsmith:version` comment in `ui/go.mod` and writes `ui/CHANGELOG.md`.
2. `shiprig tag` renders `ui/vX.Y.Z` from the module directory — it has always
   done this; nothing consumed the tag until now.
3. Pushing it fires `.github/workflows/release-ui.yml`: GoReleaser builds and
   Authenticode-signs the Windows binaries from `.goreleaser.ui.yaml`, a macOS
   runner builds, signs and notarizes the `.app` via `scripts/package-ui.sh`,
   and the two are published together as one GitHub release, a Homebrew cask and
   a winget submission.

`scripts/ui-release-version.sh` refuses a tag that disagrees with `ui/go.mod`.
The two are written at different moments, and a mismatch would ship a window
reporting one number under a tag promising another — with the cask and the
winget manifest each believing a different one.

Actions → **Release the UI** → Run workflow is a dry run: it builds and signs
with the real secrets, publishes nothing, marks the version `-dryrun`, and
uploads the artifacts.

GoReleaser builds here but never publishes. `ui/v0.2.0` is not a semver tag and
OSS GoReleaser cannot be told about a prefix (`monorepo.tag_prefix` is a Pro
feature), so the run is a snapshot and the workflow creates the release. The
macOS half has always worked that way — a cgo `.app` bundle is not something
GoReleaser builds.
