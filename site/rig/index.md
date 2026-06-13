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

rig is **convention-first**: it works with zero configuration. An optional
[`.rig.json`](./configuration) supplies only what can't be inferred.

The dev verbs map through each ecosystem's `DevCommands` (shared with relrig),
so an ecosystem declares its own commands. Ecosystems that don't define
`lint`/`typecheck` report "no mapping" cleanly.

Global flags: `--dry-run`/`-n` (print what would run, don't run it) and
`--quiet`/`-q` (suppress the `→ command` echo).

- [All verbs →](./verbs)
- [Configuration (`.rig.json`) →](./configuration)
