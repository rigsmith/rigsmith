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
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `remove` / `outdated` / `upgrade` | Package management, native per ecosystem |
| `install` / `ci` / `clean` / `rebuild` | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | dotnet publish with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `worktree` / `wt` | Sibling git worktrees (`new`/`list`/`open`/`rm`/`prune`), delegating to `clauderig worktree` |
| `branch` / `br` | Local branches (`list`/`rm`/`prune`, `--gone`), delegating to `clauderig branch` |
| `prune` / `tidy` | One sweep: reap merged worktrees then their branches, delegating to `clauderig prune` |
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
precedence is `.rig.json` > solution/workspace manifest > git root) supplies
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
| `commands` | map | Custom verbs: shell string, argv array, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description` |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb is ignored. Config writes (e.g. the
default-setter) preserve comments via the JSONC editor.

## Roadmap

The remaining ergonomics tail (`[suggest]` completion, menu project-pickers,
`setup`/`self-update`, the interactive `default` verb, test-class fuzzy match)
is tracked in [../docs/FEATURE-PARITY.md](../docs/FEATURE-PARITY.md).
