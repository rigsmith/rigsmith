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

`--window` opens the status window at startup and `--sessions` the sessions
manager. Without either the app starts in the tray only — which is the intended
behaviour, and also the escape hatch for Linux desktops where the tray never
appears (GNOME needs an AppIndicator extension).

`CLAUDERIG_TERMINAL` names the application the sessions window's **Open in
terminal** button hands the resume script to; it defaults to `Terminal`, which
is the one macOS always has. The **Copy command** button beside it is the path
that works with any terminal, multiplexer or remote host.

Both flags reveal their window on `events.Common.ApplicationStarted` rather than
before `app.Run()`. Showing a window before the app is running silently does
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
| `frontend/dist/index.html` | the status window, plain HTML/CSS/JS on the `design/` tokens |
| `frontend/dist/sessions.html` | the sessions manager window |

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
