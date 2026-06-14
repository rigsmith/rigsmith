# Installation

Every RigSmith tool is a single, statically-linked Go binary — no .NET runtime,
no Node. Install the whole family or just one tool.

## curl | sh

```sh
curl -fsSL https://rigsmith.sh | sh            # the whole family
curl -fsSL https://rigsmith.sh/rig | sh        # just rig
curl -fsSL https://rigsmith.sh/shiprig | sh    # just shiprig
curl -fsSL https://rigsmith.sh/clauderig | sh  # just clauderig
```

Binaries install to `~/.local/bin` by default (override with `RIGSMITH_INSTALL`).
Make sure that directory is on your `PATH`.

::: tip Auditing the script
`https://rigsmith.sh` returns the install script as plain text — open it in a
browser to read it before piping it to a shell.
:::

## Homebrew

```sh
brew install rigsmith/tap/rig
brew install rigsmith/tap/shiprig
```

## Scoop (Windows)

```sh
scoop bucket add rigsmith https://github.com/rigsmith/scoop-bucket
scoop install rig
```

## From source

The repo is a `go.work` workspace; build any module from its directory:

```sh
go build -o bin/rig       ./cli
go build -o bin/changerig ./changerig
go build -o bin/shiprig   ./shiprig
go build -o bin/clauderig ./clauderig
```

`clauderig` additionally needs `git` and an authenticated GitHub CLI (`gh`) for
its private-repo gate.

::: warning Status
The native package channels (Homebrew, Scoop, the `rigsmith.sh` installer) are
being wired up. Until the first tagged release, build from source.
:::
