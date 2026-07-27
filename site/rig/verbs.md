# Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` / `dev` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `verify` | `build` → `test` → `run` in sequence, then check the artifacts were built together ([see below](#verify)) |
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `uninstall` (`remove`, `rm`) / `outdated` (`od`) `[project]` / `upgrade` | Package management, native per ecosystem. On .NET, `outdated` reviews **every** project in the repo (respecting `exclude`), grouped by project, so a stale package in any in-repo dependency surfaces; name a `[project]` to scope it, like `run` |
| `deps` / `dependencies` | List dependencies with current + latest versions (`-u` updates only, `--vulnerable`, `--json`); whole-repo (per-project) on .NET |
| `install` (`restore`) / `ci` / `clean` / `rebuild` (`rb`) | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` / `x` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | `dotnet publish` with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`), headed by a `Setup` group: which rig is running and from where, the family on `PATH`, and what's registered in your shell ([see below](#doctor-setup)) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `init` | Scaffold a `.rig.json` |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) — plus a `Warnings` section for anything wrong with the config |
| `explain [verb]` | Show what a verb resolves to — command, directory, environment, source — without running it ([see below](#explain)) |
| `config` | Manage `.rig.json` (`get` / `set` / `show` / `path` / `edit`) |
| `default` | Show or set the default project for `run`/`publish` (interactive picker) |
| `setup` | Install shell integration — `cd` wrapper + tab completion (zsh/bash/fish/PowerShell); `--aliases` also adds the short [verb aliases](./alias) |
| `alias` | Install short verb aliases — `rr` run, `rb` build, `rt` test, `rcd` cd, … ([details](./alias)) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand — shell string, argv, or a cross-platform Tengo [`script`](/rig/configuration#commands) |
| *scripts* | In a Node repo, every `package.json` script becomes a verb |

## What rig itself has installed {#doctor-setup}

`rig doctor` opens with a `Setup` group about rig rather than your project:

```
Setup
  ✓ rig        1.4.2 · /Users/john/.local/bin/rig
  ✓ family     shiprig, changerig, clauderig · /Users/john/.local/bin
  ! shell      not installed in ~/.zshrc — `rig cd` can't change your directory
               and tab completion is off; run `rig setup zsh`
  ✓ aliases    4 of 11 installed: rr, rb, rt, rcd · ~/.zshrc
```

- **rig** — the running binary's version and path. Two copies on `PATH` is a
  warning: the first one answers `rig`, and it may not be the one you upgraded.
  Running a build by path (a source build, a `-dev` launcher) is normal, so it
  says what typing `rig` would run instead of faulting it.
- **family** — which sibling rigs are installed and where. Never a warning:
  each one's completion is loaded only when present, so installing one later
  needs a new shell, not a re-run of `rig setup`.
- **shell** — whether the `rig setup` block is in your startup file, and whether
  it's the one this rig would write. Absent or stale is a warning, because both
  of the things it provides fail silently. Having only a `--dev` block is called
  out by name: it's the state behind "I ran setup and `rig cd` still does
  nothing".
- **aliases** — which of `rr`, `rb`, `rt`, … are actually live, from the same
  block `rig alias install` writes.

The group runs everywhere, including a directory with no project — that is
exactly where you stand when asking why `rcd` does nothing. Nothing here is
touched by `rig doctor --fix`: writing to your startup file is `rig setup`'s
job, and it asks.
## Proving a result: `verify` {#verify}

`build`, `test` and `run` each answer their own question honestly, and the
answers can still be collectively wrong — because nothing checks that the
artifacts in play were produced *together*. A test binary two hours older than
the resources it loads passes its own build check and then crashes in code
nobody touched.

`rig verify` does two jobs:

```sh
rig verify                     # build → test → run, stopping at the first failure
rig verify --stale-only        # report disagreement, run nothing
rig verify --no-run            # build and test only
rig verify --run-timeout 30s   # how long "it starts" is given to prove itself
```

**Sequencing** makes "I checked" mean one thing instead of three. Each step is
the *same* command the standalone verb runs — `verify` reuses `build`/`test`/`run`
rather than reproducing how they resolve a target, so the two can never disagree
about what ran.

**Agreement** is the valuable half. Sequencing alone doesn't solve the problem,
it hides it, by rebuilding everything every time — fine for a Go service,
unusable where a build takes minutes to hours, which is exactly where stale
artifacts survive longest. So `verify` compares modification times instead:

- With **no configuration**, the generic check: is anything under the source
  tree newer than the newest build output? (Source means files a build actually
  consumes — editing a README doesn't count.) Output locations follow the
  ecosystem: `bin`/`dist` for Go, `dist`/`build`/`out`/`.next` for Node,
  per-project `bin/<config>/<tfm>` for .NET, `target/<profile>` for Cargo. Node
  also gets `node_modules` checked against its lockfile.
- With an **`artifacts` block** in `.rig.json`, the artifacts rig cannot infer —
  generated resources, multi-artifact builds, an `out/` tree beside the repo
  ([configuration](./configuration#artifacts)).

```
  ✓ build output  up to date with main.go
  ✗ browser       out/App.app/Contents/Resources/en.pak is 2h older than src/strings.grd (and 1 more file)
  ✗ unit-tests    out/unit_tests is 2h older than src/renderer.cc
```

Notes on the guarantees, because a check that exits zero while being wrong is
worse than no check:

- **Staleness is a failure, not a warning** — `verify` exits non-zero, so it can
  gate CI or a pre-push hook. A warning in a long log is what got missed.
- **Checks that could not run are reported as skipped**, never counted as
  passes; when nothing could be checked, the summary says so instead of printing
  a green line.
- **Nothing is rebuilt implicitly.** `--stale-only` reports and stops; the full
  `verify` rebuilds by construction.
- **A directory artifact is judged by its OLDEST file.** An app bundle whose
  newest file is minutes old can still hold a resource the build never
  refreshed — that bundle looks fresh and loads stale data.
- **The run step passes by staying alive.** A server or a desktop app never
  exits, so "still running after `--run-timeout`" (default 10s) is the answer to
  "does it start". An exit with a non-zero status before then is a failure. Set
  `verify.run: false` (or pass `--no-run`) where launching isn't wanted.
- **The run step is cleaned up completely.** It runs in its own process group,
  so the timeout takes down everything it started — `go run`'s compiled binary,
  a dev server's child processes — rather than reporting "it starts" and leaving
  one behind holding the port.

## Passing flags to the underlying tool

rig owns a small set of flags per verb (`--all`, `--filter`, `--watch`, `-i`, plus
the global `--dry-run` / `--quiet` / `--no-env` / `--root`). Anything else is
rejected rather than guessed at, so a typo like `--dry-runn` is caught instead of
being handed to your package manager. To reach the tool underneath, put the flag
after `--`:

```sh
rig build -- --target=host        # → pnpm run build --target=host
rig test -- --reporter=dot        # → npm run test --reporter=dot
rig dlx prettier -- --write .     # → pnpm dlx prettier --write .
```

Everything after `--` is forwarded verbatim and never interpreted — it is not
read as a project name or a test-class query. Forget it and rig says so, naming
the flag and quoting your own command line back with the `--` already in place:

```
  ERROR

  Unknown flag: --target.

  rig build doesn't take --target. To pass it to the underlying command, put it after --:

      rig build -- --target=host
```
## Reading a verb before you run it {#explain}

`rig explain <verb>` prints what the verb resolves to and stops there:

```
$ rig explain markers
Verb
  name:    markers
  source:  custom command · /repo/.rig.json

Command
  runs:    grep -rho 'sheepish-[a-z-]*' src | sort -u
  shell:   portable · rig's in-process POSIX shell, same on every OS
  dir:     /repo

Environment
  API_TOKEN=abc      · .env / .env.local
  SHEEPISH_ROOT=src  · .rig.json env
  the rest is inherited from the current environment

  nothing ran — `rig markers` runs it
```

A custom command can be valid JSON wrapping a valid shell line that quietly does
the wrong thing — a character class missing `0-9`, a `grep -h` upstream of a
filter that matches on filenames — and it exits 0 while printing something
plausible. Nothing can validate that for you, but a resolved command you can
read takes seconds to check.

Bare `rig explain` lists every verb the repo resolves — the ecosystem's dev
loop, your `commands`, the `package.json` scripts and the script directories —
each with the command it becomes.

The resolution comes from the same code the verb runs through, not a second
implementation, so what you read is what executes. Verbs that decide part of
their command while running (`coverage`, `rebuild`, `publish`, `upgrade`,
`outdated`) are not guessed at: explain says so, and for the ones whose
`--dry-run` prints an exact command it points there, since that goes through the
real path. A few verbs have no such contract — `info` has no underlying command,
and `outdated` runs its scans without echoing them — so explain says only that
it cannot show a guaranteed answer. The same is true for an
argument that selects a project or a test filter — `rig test MyClass --dry-run`
rather than `rig explain test MyClass`.

## Ecosystem coverage

The same verb runs the native tool for your stack. A few combinations have no
native equivalent and degrade gracefully — with a clear message — rather than
failing:

- **.NET** has no separate `typecheck` (it would just be `build`).
- **Cargo** has no `dlx` one-shot runner, and `deps` falls back to the plain
  `cargo outdated` output rather than the rich table.
- **Node** `clean` runs only when the package defines a `clean` script.

The full per-ecosystem matrix lives in [`docs/ECOSYSTEM-MATRIX.md`](https://github.com/rigsmith/rigsmith/blob/main/docs/ECOSYSTEM-MATRIX.md).

## Git & worktree verbs

| Verb | What |
|------|------|
| `copy` / `cp` | Detached copy of the repo tree to a new folder; `--git` keeps `.git` history |
| `worktree` / `wt` | Parallel-dev sibling worktrees: `new` / `list` / `open` / `rm` (the menu/list show age, newest-first). Direct branch management is left to `git`/`gh` |
| `prune` / `tidy` | One sweep that reaps merged + gone-upstream **worktrees and branches** (worktrees first). `--worktrees` / `--branches` scope it; at the confirm prompt `w`/`b`/`a` retarget in place. `-n` previews, `-y` skips the prompt; off a terminal it refuses without `-y`. `--keep-gone` keeps gone-upstream items |

```sh
rig worktree new feat/x          # sibling checkout off mainline (prints the path)
rig worktree new feat/x --open   # …and open a review window for this run
rig worktree new feat/x --repo ~/Git/other   # act on another repo without cd'ing there
rig worktree list                # this repo's worktrees, newest-first (alias: ls)
rig copy ../scratch --git        # detached copy that keeps history
```

See [claudeRig — worktree discipline](/clauderig/commands#worktree-discipline)
for how the guard makes worktrees + PRs the default under Claude Code, and
[Configuration](./configuration#worktree) for the `worktree.autoOpen` /
`worktree.openCmd` keys.

## Prefix matching

Verbs prefix-match, so `rig cove` runs `coverage` and `rig w r` is `watch run`.
The watch modifier rides the same pre-parse pipeline, so it composes with any
verb.

## Discovered verbs

In a Node repo, every `package.json` script becomes a verb. In a Go repo, any
`main` package under `scripts/` or `cmd/` is surfaced as a bare `rig <name>`
verb — these are exact-match only (excluded from prefix-matching) and never
shadow a built-in. `rig run` offers those `cmd/*` binaries directly instead of
falling through to a doomed `go run .`.

## How a project name resolves {#project-resolution}

`rig run <name>`, `rig build <name>`, `rig cd <name>` and a configured
[`defaultProject`](./configuration) all resolve through the same rules, in this
order:

1. **Best tier wins, and it stops there** — exact, then prefix, then substring
   (`rig cd` also accepts subsequences). One exact hit is never ambiguous just
   because looser matches exist: `Tweed.App` is that project, not it plus
   `Tweed.App.Tests`. Names match in full, slash-short (`@scope/pkg` → `pkg`),
   and dot-short (`Acme.Desktop` → `Desktop`) form.
2. **Ties break by proximity to your working directory** — the project you're
   standing in wins, then the nearest common ancestor.
3. **Anything still tied is ambiguous, and rig says so** — a picker on a
   terminal; off one, the candidate paths, what each copy is (a
   [nested worktree](./configuration#nested-worktrees), and whether `rig prune`
   would remove it), and the exact `exclude` line to paste into `.rig.json`.

A bare `rig run` obeys **the same rules** as `rig run <name>`. A
`defaultProject` matching two checkouts fails exactly as the explicit command
would rather than launching whichever was found first, and when it resolves
cleanly rig echoes what it picked:

```
· defaultProject Tweed.App → ui/src/Tweed.App
```

## Opening the picker (`-i` / `--interactive`)

At a workspace root where targets live only in subdirectories, a bare `rig run`
(or `build`/`test`/`format`/`lint`/`typecheck`/`clean`/`rebuild`) opens a picker —
no flag needed. `run` lists the runnable packages **and** the repo's surfaced
scripts; the other verbs list packages only. When one obvious target *would* run
directly, pass `-i`/`--interactive` to force the picker anyway:

```sh
rig run                  # picker only when there's no single target
rig run -i               # always pick, even with one obvious target
rig build --interactive  # same, for the --all-capable verbs
rig rebuild -i           # rebuild a chosen package, or "All packages"
```

`rebuild` carries its own picker (it sequences clean → build, so it has no single
command to ride the shared one): `rig rebuild <project>` scopes the rebuild to one
package, and the picker's **All packages** rebuilds each in dependency order.

Off a TTY there's no picker, so `-i` reports a helpful error and points you at
`rig <verb> <project>`.

## Picker controls (exclude / include)

When `rig run` (or the `rig ui` project menu) lists several projects, you can
curate the set live:

- **`x`** — exclude the highlighted project from future discovery. In a crowded
  directory (≥3 siblings) it asks whether to hide just that project or the whole
  `<dir>/*`.
- **`i`** — show/hide excluded projects; while shown they appear struck-through,
  and pressing `i` on one re-includes it.

Exclusions are written to `.rig.json`'s [`exclude`](./configuration) globs and
match against the project's full name, short name, and repo-relative path.
