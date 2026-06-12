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

## Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) |
| `ui` | Interactive bubbletea menu over the dev verbs |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand |

The dev verbs map through each ecosystem's `EcosystemInfo.DevCommands` (shared
with relrig), so an ecosystem declares its own commands. Ecosystems that don't
define `lint`/`typecheck` report "no mapping" cleanly.

Global flags: `--dry-run`/`-n` (print what would run, don't run it) and
`--quiet`/`-q` (suppress the `→ command` echo).

## Configuration (`.rig.json`, all optional)

rig is convention-first — it works with **zero configuration**. An optional
`.rig.json` at the repo root (found by walking up from cwd) supplies only what
can't be inferred. Plain JSON (no JSONC); a missing file is fine.

```json
{
  "defaultProject": "MyApp",
  "quiet": false,
  "exclude": ["*.Bench", "*.Demo"],
  "env": { "MYAPP_LOG": "1" },
  "commands": { "deploy": "./deploy.sh" }
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `defaultProject` | string | Project to act on when several are runnable |
| `quiet` | bool | Suppress the `→ command` echo (same as `--quiet`) |
| `exclude` | []string (globs) | Hide projects from discovery/pickers |
| `env` | map[string]string | Extra environment for spawned commands |
| `commands` | map[string]string | Custom verbs (npm-scripts style); each runs its shell string via `sh -c` |

Custom commands honor `--dry-run`; unknown flags and extra args are forwarded to
the script. A custom name that collides with a built-in verb is ignored.

## Roadmap

The full parity surface (coverage, kill, package management, completion,
cross-ecosystem delegation, `env`/`exclude` enforcement) is tracked in
[../docs/PORTING-PLAN.md](../docs/PORTING-PLAN.md).
