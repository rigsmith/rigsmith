# Installation

Every RigSmith tool is a single, statically-linked Go binary — no .NET runtime,
no Node. The same four tools run natively on **macOS, Linux, and Windows**, on
both x86-64 and Arm64 (Apple Silicon, Windows on Arm, arm64 Linux). Every
release ships all six builds of every tool at once, so no platform trails the
others.

| Your platform | Install with |
| --- | --- |
| **Windows** | [winget](#winget-windows) · [Scoop](#scoop-windows) · [PowerShell](#powershell-windows) |
| **macOS** | [Homebrew](#homebrew-macos-linux) · [curl \| sh](#curl-sh-macos-linux) |
| **Linux** | [Homebrew](#homebrew-macos-linux) · [curl \| sh](#curl-sh-macos-linux) |

Winget, Homebrew, the install scripts, and direct downloads offer the same choice:
install the whole family or just one tool. Scoop provides the family bundle.

## winget (Windows)

```powershell
winget install RigSmith.Rigsmith    # all four tools
winget install RigSmith.Rig         # just rig
winget install RigSmith.ChangeRig   # just changerig
winget install RigSmith.ShipRig     # just shiprig
winget install RigSmith.ClaudeRig   # just clauderig
```

These are portable packages — winget unpacks the `.exe`s and registers each one
as a command on your `PATH`. Restart the terminal to pick that up. Both x64 and
Arm64 installers are published for every package.

## Scoop (Windows)

```powershell
scoop bucket add rigsmith https://github.com/rigsmith/scoop-bucket
scoop install rigsmith             # all four tools
```

## PowerShell (Windows)

```powershell
irm https://rigsmith.sh | iex             # the whole family
irm https://rigsmith.sh/rig | iex         # just rig
irm https://rigsmith.sh/changerig | iex   # just changerig
irm https://rigsmith.sh/shiprig | iex     # just shiprig
irm https://rigsmith.sh/clauderig | iex   # just clauderig
```

Binaries install to `$HOME\.local\bin` (override with `RIGSMITH_INSTALL`); the
script adds that directory to your user `PATH` — restart the terminal to pick it
up. Same URL as curl: PowerShell gets the `.ps1`, a shell gets the `.sh`.

## Homebrew (macOS / Linux)

```sh
brew install --cask rigsmith/tap/rigsmith   # all four tools
brew install --cask rigsmith/tap/rig        # just rig
brew install --cask rigsmith/tap/changerig  # just changerig
brew install --cask rigsmith/tap/shiprig    # just shiprig
brew install --cask rigsmith/tap/clauderig  # just clauderig
```

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

## Direct download

Every [GitHub release](https://github.com/JohnCampionJr/rigsmith/releases)
attaches a per-tool archive and a combined `rigsmith_<version>_<os>_<arch>`
archive for each of the six targets — `darwin`, `linux`, and `windows` × `amd64`
and `arm64` — plus a `checksums.txt`. Unpack and put the binaries on your `PATH`.

## From source

The repo is a single Go module (`github.com/rigsmith/rigsmith`) — the four
binaries live under `cmd/`, the shared engine under `core/`. Build any binary
from the repo root, on any OS Go supports:

```sh
go build -o bin/rig       ./cmd/rig
go build -o bin/changerig ./cmd/changerig
go build -o bin/shiprig   ./cmd/shiprig
go build -o bin/clauderig ./cmd/clauderig
```

`clauderig` additionally needs `git` and an authenticated GitHub CLI (`gh`) for
its private-repo gate.
