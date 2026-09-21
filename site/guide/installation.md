# Installation

Every RigSmith tool is a single, statically-linked Go binary — no .NET runtime,
no Node. `rig`, `changerig`, `shiprig`, `clauderig` and `codexrig` run natively
on **macOS, Linux, and Windows**, on both x86-64 and Arm64 (Apple Silicon,
Windows on Arm, arm64 Linux), and every release ships all six builds of each at
once so no platform trails the others.

`brewrig` is the exception: **macOS and Linux only**, four builds, because
Homebrew does not run on Windows.

| Your platform | Install with |
| --- | --- |
| **Windows** | [winget](#winget-windows) · [Scoop](#scoop-windows) · [PowerShell](#powershell-windows) |
| **macOS** | [Homebrew](#homebrew-macos-linux) · [curl \| sh](#curl-sh-macos-linux) |
| **Linux** | [Homebrew](#homebrew-macos-linux) · [curl \| sh](#curl-sh-macos-linux) |

Winget, Homebrew, the install scripts, and direct downloads offer the same choice:
install the whole family or just one tool. Scoop provides the family bundle.

Being macOS/Linux only, `brewrig` is absent from the winget and Scoop lanes. The
install scripts leave it out of a whole-family install on Windows rather than
failing, and say why if you ask for it by name; so does `rigsmith brewrig` from
the npm meta package.

## winget (Windows)

```powershell
winget install RigSmith.Rigsmith    # the whole family
winget install RigSmith.Rig         # just rig
winget install RigSmith.ChangeRig   # just changerig
winget install RigSmith.ShipRig     # just shiprig
winget install RigSmith.ClaudeRig   # just clauderig
winget install RigSmith.CodexRig    # just codexrig
```

These are portable packages — winget unpacks the `.exe`s and registers each one
as a command on your `PATH`. Restart the terminal to pick that up. Both x64 and
Arm64 installers are published for every package.

## Scoop (Windows)

```powershell
scoop bucket add rigsmith https://github.com/rigsmith/scoop-bucket
scoop install rigsmith             # the whole family (no brewrig — see above)
```

## PowerShell (Windows)

```powershell
irm https://rigsmith.sh | iex             # the whole family
irm https://rigsmith.sh/rig | iex         # just rig
irm https://rigsmith.sh/changerig | iex   # just changerig
irm https://rigsmith.sh/shiprig | iex     # just shiprig
irm https://rigsmith.sh/clauderig | iex   # just clauderig
irm https://rigsmith.sh/codexrig | iex    # just codexrig
```

Binaries install to `$HOME\.local\bin` (override with `RIGSMITH_INSTALL`); the
script adds that directory to your user `PATH` — restart the terminal to pick it
up. Same URL as curl: PowerShell gets the `.ps1`, a shell gets the `.sh`.

## Homebrew (macOS)

```sh
curl -fsSL rigsmith.sh/brew | sh                 # the whole family
curl -fsSL rigsmith.sh/brew/clauderig | sh       # just clauderig
curl -fsSL rigsmith.sh/brew/codexrig | sh        # just codexrig
curl -fsSL rigsmith.sh/brew/brewrig | sh         # just brewrig
curl -fsSL rigsmith.sh/brew/clauderig-ui | sh    # the menu bar app
```

Or run brew yourself:

```sh
brew install --cask rigsmith/tap/rigsmith      # the whole family
brew install --cask rigsmith/tap/rig           # just rig
brew install --cask rigsmith/tap/changerig     # just changerig
brew install --cask rigsmith/tap/shiprig       # just shiprig
brew install --cask rigsmith/tap/clauderig     # just clauderig
brew install --cask rigsmith/tap/codexrig      # just codexrig
brew install --cask rigsmith/tap/brewrig       # just brewrig
brew install --cask rigsmith/tap/clauderig-ui  # the menu bar app
```

Note the `rigsmith/tap/` prefix. Homebrew will not load a cask from a
non-official tap by its short name until the tap is trusted, so
`brew install rig` stops with a trust prompt while the qualified name does not.
That is the whole reason `rigsmith.sh/brew` exists — it bakes the prefix in.

The packages are casks, so this route is macOS only. On Linux use `curl | sh`
below.

## curl | sh (macOS / Linux)

```sh
curl -fsSL https://rigsmith.sh | sh            # the whole family
curl -fsSL https://rigsmith.sh/rig | sh        # just rig
curl -fsSL https://rigsmith.sh/changerig | sh  # just changerig
curl -fsSL https://rigsmith.sh/shiprig | sh    # just shiprig
curl -fsSL https://rigsmith.sh/clauderig | sh  # just clauderig
curl -fsSL https://rigsmith.sh/codexrig | sh   # just codexrig
curl -fsSL https://rigsmith.sh/brewrig | sh    # just brewrig (macOS / Linux)
```

Binaries install to `~/.local/bin` by default (override with `RIGSMITH_INSTALL`).
Make sure that directory is on your `PATH`.

::: tip Auditing the script
`https://rigsmith.sh` returns the install script as plain text — open it in a
browser to read it before piping it to a shell.
:::

## Direct download

Every [GitHub release](https://github.com/rigsmith/rigsmith/releases)
attaches a per-tool archive and a combined `rigsmith_<version>_<os>_<arch>`
archive for each of the six targets — `darwin`, `linux`, and `windows` × `amd64`
and `arm64` — plus a `checksums.txt`. (`brewrig` has four: no `windows`, and the
Windows bundle omits it.) Unpack and put the binaries on your `PATH`.

## From source

The repo is a single Go module (`github.com/rigsmith/rigsmith`) — the binaries
live under `cmd/`, the shared engine under `core/`. Build any binary from the
repo root, on any OS Go supports:

```sh
go build -o bin/rig       ./cmd/rig
go build -o bin/changerig ./cmd/changerig
go build -o bin/shiprig   ./cmd/shiprig
go build -o bin/clauderig ./cmd/clauderig
go build -o bin/codexrig  ./cmd/codexrig
go build -o bin/brewrig   ./cmd/brewrig   # macOS / Linux
```

`clauderig`, `codexrig` and `brewrig` additionally need `git` and an
authenticated GitHub CLI (`gh`) for their private-repo gate. `brewrig` needs
Homebrew itself, which is also why it has no Windows build.
