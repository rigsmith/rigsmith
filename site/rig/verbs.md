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
| `global` / `dlx` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | `dotnet publish` with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `init` | Scaffold a `.rig.json` |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand |
| *scripts* | In a Node repo, every `package.json` script becomes a verb |

## Prefix matching

Verbs prefix-match, so `rig cove` runs `coverage` and `rig w r` is `watch run`.
The watch modifier rides the same pre-parse pipeline, so it composes with any
verb.

## Discovered verbs

In a Node repo, every `package.json` script becomes a verb. In a `go.work`
workspace, any `main` package under `scripts/` or `cmd/` is surfaced as a bare
`rig <name>` verb — these are exact-match only (excluded from prefix-matching)
and never shadow a built-in.
