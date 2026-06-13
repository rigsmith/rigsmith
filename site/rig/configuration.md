# Configuration

rig is convention-first — it works with **zero configuration**. An optional
`.rig.json` at the repo root (found by walking up from cwd; the root anchor
precedence is `.rig.json` > solution/workspace manifest > git root) supplies
only what can't be inferred.

It's **JSONC** (comments + trailing commas welcome); a missing file is fine;
unknown keys get a did-you-mean warning.

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
