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
| `exclude` | []string (globs) | Hide projects from discovery/pickers (also written by the picker's `x` key) |
| `env` | map | Extra environment; layered file (`.env`/`.env.local`) < ambient < config < command |
| `coverage.*` / `publish.*` / `rebuild.skip` / `kill.match` | — | Verb defaults (flags win) |
| `worktree.autoOpen` / `worktree.openCmd` | bool / string | Whether `rig worktree new` opens a review window, and the command to open it ([see below](#worktree)) |
| `commands` | map | Custom verbs: shell string, argv array, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description` |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb is ignored. Config writes (e.g. the
default-setter and the picker's exclude/include keys) preserve comments via the
JSONC editor.

## Excluding projects {#exclude}

`exclude` hides projects from discovery and the pickers. You can edit it by hand
or let the [`run`/`ui` picker](./verbs#picker-controls-exclude-include) write it
for you (`x` to exclude, `i` to show/re-include). Globs match against each
project's full name, short name, and repo-relative path:

```jsonc
{ "exclude": ["*.Bench", "*.Demo", "examples/*"] }
```

## Worktrees {#worktree}

`rig worktree new` can open the new sibling checkout in a separate review window.
Both *whether* it opens and *what* opens it are configurable:

```jsonc
{
  "worktree": {
    "autoOpen": true,          // default false; --open / --no-open override per run
    "openCmd": "cursor -n"     // default "code -n"; e.g. "subl -n", "idea"
  }
}
```

The worktree path is appended as the final argument to `openCmd` and run
directly (no shell). When the opener isn't on `PATH`, rig prints the command to
run instead.

## .NET repository discovery

rig detects .NET repos even when there's no solution or `.csproj` at the root, by
recognizing conventional markers (`Directory.Build.props`,
`Directory.Build.targets`, `Directory.Packages.props`, `global.json`,
`nuget.config`). Projects can live in subdirectories — `rig run` / `build` /
`test` discover them and offer a subproject picker even when no single primary
ecosystem resolves.

## Embedding shiprig / changerig config {#embedded}

`.rig.json` can also carry a sibling tool's config as a top-level key, so a repo
that prefers one file can keep everything here instead of in `.changeset/`:

- `"shiprig"` (or `"release"`) — the [release pipeline](/shiprig/pipeline) config
- `"changerig"` (or `"changeset"`) — the [changeset](/changerig/lifecycle) config

Each tool also still reads its standalone files; provide the config in **exactly
one** place — if a tool finds it both here and in a standalone file, it stops and
lists the conflict rather than guessing.
