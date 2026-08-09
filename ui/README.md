# clauderig UI

The ambient face of clauderig: a menu bar icon that colours itself from real
sync state, plus a window for the detail. Design and phasing live in
[docs/CLAUDERIG-UI-PLAN.md](../docs/CLAUDERIG-UI-PLAN.md).

Wails v3 (`v3.0.0-beta.5`, pinned), Go backend, platform webview frontend.

## Build and run

```sh
MACOSX_DEPLOYMENT_TARGET=12.0 \
CGO_ENABLED=1 \
CGO_LDFLAGS="-O2 -g -mmacosx-version-min=12.0" \
  go run ./ui --window
```

`--window` opens the window at startup. Without it the app starts in the tray
only — which is the intended behaviour, and also the escape hatch for Linux
desktops where the tray never appears (GNOME needs an AppIndicator extension).

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
| `assets/` | tray icons, three states × light/dark ([README](assets/README.md)) |
| `frontend/dist/` | the window, plain HTML/CSS/JS on the `design/` tokens |

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
which would otherwise compile clean and break only at runtime.
