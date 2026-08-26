# rig

RigSmith's convention-first dev launcher — the Go successor to the .NET/Node
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

rig is **convention-first**: it works with zero configuration. An optional
[`.rig.json`](./configuration) supplies only what can't be inferred.

The dev verbs map through each ecosystem's `DevCommands` (shared with shipRig),
so an ecosystem declares its own commands. Ecosystems that don't define
`lint`/`typecheck` report "no mapping" cleanly.

Global flags: `--dry-run`/`-n` (print what would run, don't run it),
`--quiet`/`-q` (suppress the `→ command` echo), `--no-env` (skip
`.env`/`.env.local` loading for this run), `--root <dir>` (override the
working root, skipping walk-up discovery), and `--include-worktrees` (also
discover projects inside [nested git worktrees](./configuration#nested-worktrees),
which are skipped by default).

Beyond the dev loop, rig manages [parallel worktrees](./verbs#git--worktree-verbs)
and [stack workspaces](./verbs#stack) — several forked repos fused into one
history, so a change can span them in a single commit and still leave as an
ordinary pull request to each.

- [All verbs →](./verbs)
- [Configuration (`.rig.json`) →](./configuration)
