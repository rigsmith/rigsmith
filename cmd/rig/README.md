# rig

rigsmith's convention-first dev launcher — the Go successor to the .NET/Node
`rig`. The same verb works in any ecosystem; rig detects the repo and runs the
right native command.

```sh
rig info                 # what rig discovered (config, dev commands, packages)
rig ui                   # interactive menu over the dev verbs
rig build                # → go build ./...  | dotnet build | npm run build
rig test
rig run
rig format
rig lint
rig typecheck
rig build --dry-run      # print the command, don't run it
rig build --quiet        # suppress the → command echo
```

Beyond the dev loop it manages parallel worktrees, and **stackspaces** —
your app and the libraries you have forked, fused into one history, so a change
can span them in a single commit while each still leaves as itself. Private
repos need no setup beyond `gh auth login`:

```sh
rig stack setup                      # a fresh clone: engine, members, overlay, status
rig stack init                       # scaffold the manifest; run again to import
rig stack add github.com/them/lib    # fuse another repo
rig stack rm some-lib                # take one back out: manifest, tree and overlay
rig stack status                     # who has moved upstream, what has not left, and where each topic's PR is
rig stack pull some-lib              # take that movement
rig stack propose some-lib read-timeout -m "Fix the read timeout"   # ALL of some-lib's changes → stack/read-timeout
rig stack propose some-lib read-timeout --from read-timeout         # ...or one topic's tree (stack-pr-read-timeout); root it on the import, or it carries its parent's fixes too
rig stack push my-app                # a repo you own: its own branch, history intact
rig stack pack some-lib              # its packages, built here — a bare checkout of a proposed branch cannot
rig stack propose some-lib read-timeout --dry-run   # the commit and fork branch that would go; touches no remote
rig stack push my-app --dry-run      # the target, branch and commits that would go; touches no remote
rig stack seed ../rigstack-acme      # portable seed; the menu suggests a path outside enclosing repos
```

`propose` proposes one squashed commit to a fork you contribute to; `push`
fast-forwards a project you own with every commit that touched it. Mark a
project `"owned": true` in the manifest to enable the second.

## Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `verify` | `build` → `test` → `run` in sequence, then check the artifacts were built together (`--stale-only` checks only, `--no-run` skips the launch) |
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `remove` / `outdated` / `upgrade` | Package management, native per ecosystem |
| `install` / `ci` / `clean` / `rebuild` | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | dotnet publish with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `worktree` / `wt` | Parallel-dev worktrees (`new`/`list`/`open`/`rm`/`prune`) + pin the active `-dev` route (`use`/`active`/`unset`) |
| `branch` / `br` | Local branches (`list`/`rm`/`prune`, `--gone`) |
| `prune` / `tidy` | One sweep: reap merged worktrees then their branches |
| `init` | Scaffold a `.rig.json` |
| `config` | `get` / `set` / `path` / `edit` the `.rig.json` (comment-preserving writes) |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand |
| *scripts* | In a Node repo, every package.json script becomes a verb |

The dev verbs map through each ecosystem's `EcosystemInfo.DevCommands` (shared
with shipRig), so an ecosystem declares its own commands. Ecosystems that don't
define `lint`/`typecheck` report "no mapping" cleanly.

Global flags: `--dry-run`/`-n` (print what would run, don't run it) and
`--quiet`/`-q` (suppress the `→ command` echo).

## Configuration (`.rig.json`, all optional)

rig is convention-first — it works with **zero configuration**. An optional
`.rig.json` at the repo root (found by walking up from cwd; the root anchor
precedence is `.rig.json` > solution/stackspace manifest > git root) supplies
only what can't be inferred. **JSONC** (comments + trailing commas welcome);
a missing file is fine; unknown keys get a did-you-mean warning.

```jsonc
{
  "defaultProject": "MyApp",
  "quiet": false,
  "exclude": ["*.Bench", "*.Demo"],
  "env": { "MYAPP_LOG": "1" },        // layered over .env/.env.local + ambient
  "coverage": { "min": 80, "open": true },
  "publish": { "rid": "linux-x64", "selfContained": true },
  "commands": {
    "deploy": "./deploy.sh",                       // shell string
    "bench": ["go", "test", "-bench", "."],        // argv
    "open": { "os": { "macos": "open .", "windows": "explorer ." } }
  }
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `defaultProject` | string | Project to act on when several are runnable (settable via the default-setter) |
| `solution` / `test.project` | string | Pin the .NET solution / test project |
| `quiet` | bool | Suppress the `→ command` echo (same as `--quiet`) |
| `exclude` | []string (globs) | Hide projects from discovery/pickers |
| `env` | map | Extra environment; layered file (`.env`/`.env.local`) < ambient < config < command |
| `coverage.*` / `publish.*` / `rebuild.skip` / `kill.match` | — | Verb defaults (flags win) |
| `artifacts` / `verify.*` | map | What this repo builds and what it's built from (for `verify`'s staleness check); whether `verify` runs the app and for how long |
| `commands` | map | Custom verbs: shell string, argv array, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description` |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb is ignored. Config writes (e.g. the
default-setter) preserve comments via the JSONC editor.

## Roadmap

The remaining ergonomics tail (`[suggest]` completion, menu project-pickers,
`setup`/`self-update`, the interactive `default` verb, test-class fuzzy match)
is tracked in [../docs/FEATURE-PARITY.md](../docs/FEATURE-PARITY.md).

## Stackspace member CI

`rig stack ci --manifest stack/rig.stack.jsonc --member app --test "rig test"`
builds the committed checkout against sibling sources in a temporary stackspace.
Keep this job alongside the plain-clone matrix. The self-contained composite is
`.github/actions/stack/`; `rigsmith/stack-action@v1` is the intended name but is not published yet.
See [the guide](https://github.com/rigsmith/rigsmith/blob/main/docs/guides/testing-a-member-pr-in-its-stackspace.md) for a copyable workflow and other CI usage.


### Stack topic dependencies

`rig stack deps --manifest rig.stack.jsonc` checks the committed `proposals`
records through the GitHub API, using `GITHUB_TOKEN` and `GITHUB_API_URL`.
Propose upstream members first: a new proposal records the already-proposed
members of that topic in `proposals[prefix][topic].dependsOn` (an array of
member prefixes). Re-proposing preserves these edges; edit the array to change
direction or backfill older proposals. Each dependency must merge upstream and
its `lastSync` cursor must include the API-reported merge commit. `rig stack status` displays the same chain
from the local manifest. Older records without `dependsOn` have no dependencies.

`rig stack deps --publish` posts the `stack dependencies` commit status to
current PR **head** SHAs. The composite action enables this with its
`dependencies` input (default `true`). Workflows may optionally grant
`statuses: write`; read-only fork tokens produce an annotation and job summary
instead of failing for a denied write. API errors and private repositories
without access appear as **could not check**, never as passing. Do not use
`pull_request_target` to grant these writes.

Checks refresh when the command runs. No scheduled sweep is installed:
after upstream merges, commit updated cursors, then rerun dependency checking
from that manifest revision with `--publish`. Rerunning a workflow pinned to an
old manifest still evaluates its old cursors. Repositories may schedule this
command from a trusted stackspace checkout with a token authorized for each
upstream repository; without that setup, checks do not turn green by themselves.
A caller must treat pending statuses as a merge gate if desired; the reporting
command itself succeeds when dependencies are pending or unavailable.
