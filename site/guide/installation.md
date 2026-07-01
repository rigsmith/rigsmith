# Installation

Every RigSmith tool is a single, statically-linked Go binary — no .NET runtime,
no Node. Install the whole family with one command, or just one tool.

## curl | sh (macOS / Linux)

```sh
curl -fsSL https://rigsmith.sh | sh            # the whole family
curl -fsSL https://rigsmith.sh/rig | sh        # just rig
curl -fsSL https://rigsmith.sh/changerig | sh  # just changerig
curl -fsSL https://rigsmith.sh/shiprig | sh    # just shiprig
curl -fsSL https://rigsmith.sh/clauderig | sh  # just clauderig
```

Binaries install to `~/.local/bin` by default (override with `RIGSMITH_INSTALL`).
Make sure that directory is on your `PATH`.

::: tip Auditing the script
`https://rigsmith.sh` returns the install script as plain text — open it in a
browser to read it before piping it to a shell.
:::

## PowerShell (Windows)

```powershell
irm https://rigsmith.sh | iex            # the whole family
irm https://rigsmith.sh/rig | iex        # just rig
irm https://rigsmith.sh/shiprig | iex    # just shiprig
```

Binaries install to `$HOME\.local\bin` (override with `RIGSMITH_INSTALL`); the
script adds that directory to your user `PATH` — restart the terminal to pick it
up. Same URL as curl: PowerShell gets the `.ps1`, a shell gets the `.sh`.

## Homebrew (macOS / Linux)

```sh
brew install --cask rigsmith/tap/rigsmith   # all four tools
brew install --cask rigsmith/tap/rig        # just rig
```

## winget (Windows)

```powershell
winget install RigSmith.Rigsmith   # all four tools
winget install RigSmith.Rig        # just rig
```

## Scoop (Windows)

```powershell
scoop bucket add rigsmith https://github.com/rigsmith/scoop-bucket
scoop install rigsmith             # all four tools
```

## From source

The repo is a single Go module (`github.com/rigsmith/rigsmith`) — the four
binaries live under `cmd/`, the shared engine under `core/`. Build any binary
from the repo root:

```sh
go build -o bin/rig       ./cmd/rig
go build -o bin/changerig ./cmd/changerig
go build -o bin/shiprig   ./cmd/shiprig
go build -o bin/clauderig ./cmd/clauderig
```

`clauderig` additionally needs `git` and an authenticated GitHub CLI (`gh`) for
its private-repo gate.

::: warning Status
The single-tool packages (`RigSmith.Rig`, `brew … /tap/rig`, …) ship today. The
one-command **bundle** entries above — `RigSmith.Rigsmith`, the `rigsmith` cask,
`scoop install rigsmith` — land with the next tagged release; the Scoop bucket
activates once it's published.
:::
