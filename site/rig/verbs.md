# Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `remove` / `outdated` / `upgrade` | Package management, native per ecosystem |
| `install` / `ci` / `clean` / `rebuild` | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` / `x` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | `dotnet publish` with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `init` | Scaffold a `.rig.json` |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) |
| `config` | Manage `.rig.json` (`get` / `set` / `show` / `path` / `edit`) |
| `default` | Show or set the default project for `run`/`publish` (interactive picker) |
| `setup` | Install shell integration — `cd` wrapper + tab completion (zsh/bash/fish/PowerShell) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand |
| *scripts* | In a Node repo, every `package.json` script becomes a verb |

## Git & worktree verbs

| Verb | What |
|------|------|
| `copy` / `cp` | Detached copy of the repo tree to a new folder; `--git` keeps `.git` history |
| `worktree` / `wt` | Parallel-dev sibling worktrees: `new` / `list` / `open` / `rm` / `prune` (the menu/list show age, newest-first) |
| `branch` / `br` | Manage local branches: `list` / `rm` / `prune` (reaps merged **and** gone-upstream branches; `--keep-gone` keeps the latter) |
| `prune` / `tidy` | One interactive sweep: reap merged + gone-upstream worktrees, then their branches; `-n` previews, always confirms at a terminal |

```sh
rig worktree new feat/x          # sibling checkout off mainline (prints the path)
rig worktree new feat/x --open   # …and open a review window for this run
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
