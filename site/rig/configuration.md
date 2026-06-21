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
    "deploy": "./deploy.sh && echo done",          // shell string (portable shell)
    "bench": ["go", "test", "-bench", "."],        // argv (exec'd directly)
    "clean": { "script": "rm(`-rf`, `dist`); mkdir(`-p`, `dist`)" },  // tengo
    "native": { "command": "sed -i s/a/b/ f", "shell": "system" }     // OS shell
  }
}
```

| Key | Type | Meaning |
|-----|------|---------|
| `defaultProject` | string | Project to act on when several are runnable (settable via the default-setter) |
| `solution` / `test.project` | string | Pin the .NET solution / test project |
| `ecosystem` | string | Pin the primary ecosystem (`dotnet`/`node`/`go`/`cargo`) when detection is ambiguous |
| `quiet` | bool | Suppress the `→ command` echo (same as `--quiet`) |
| `exclude` | []string (globs) | Hide projects from discovery/pickers (also written by the picker's `x` key) |
| `env` | map | Extra environment; layered file (`.env`/`.env.local`) < ambient < config < command |
| `coverage.*` / `publish.*` / `rebuild.skip` / `kill.match` | — | Verb defaults (flags win) |
| `worktree.autoOpen` / `worktree.openCmd` | bool / string | Whether `rig worktree new` opens a review window, and the command to open it ([see below](#worktree)) |
| `commands` | map | Custom verbs: shell string, argv array, Tengo `script`, or object with per-OS (`macos`/`windows`/`linux`), `env`, `cwd`, `description`, `shell` ([see below](#commands)) |
| `shell` | string | How a shell-string command runs: `portable` (default, cross-platform) or `system` (the OS shell). A command's own `shell` overrides it |
| `aliases` / `dotnet.*` | — | Aliases; `dotnet`-namespaced keys fold over legacy top-level |

Custom commands honor `--dry-run`; extra args are forwarded. A custom name that
collides with a built-in verb is ignored. Config writes (e.g. the
default-setter and the picker's exclude/include keys) preserve comments via the
JSONC editor.

## Custom commands {#commands}

A `commands` entry adds a `rig <name>` verb. There are three forms:

```jsonc
"commands": {
  // 1. Shell string — runs cross-platform through an in-process portable
  //    shell, so pipes, &&/||, $VAR, globbing, and cp/mv/rm/mkdir all work the
  //    same on Linux, macOS, and Windows. No per-OS variants needed.
  "lint": "eslint . && prettier --check .",

  // 2. Argv array — exec'd directly, no shell, no quoting hazards.
  "bench": ["go", "test", "-bench", ".", "./..."],

  // 3. Tengo script — a cross-platform command body with real logic.
  "release": { "script": [
    "mkdir(`-p`, `dist`)",
    "if ctx.ecosystem == `go` { sh(`go build -o dist/app ./...`) }",
    "sh(`tar czf dist/app.tgz -C dist app`)"
  ]}
}
```

The object form takes `description`, `env`, `cwd` (relative to the repo root),
`shell`, and either `command` (string/argv) **or** `script` — not both.

### Cross-platform by default {#shell-mode}

Shell-string commands run through the **portable shell** by default — the same
mvdan.cc/sh interpreter shiprig uses — so one command line works on every OS.
Opt a command (or the whole config) back into the OS shell with `"shell":
"system"` when you need a real userland (`sed`, `awk`) or OS-specific syntax:

```jsonc
{
  "shell": "system",                                  // default for all commands
  "commands": {
    "fmt": { "command": "gofmt -w .", "shell": "portable" }  // …but this one is portable
  }
}
```

Argv-form commands are unaffected (always exec'd directly).

### Tengo scripts {#script}

The `script` form runs through rig's embedded [Tengo](https://github.com/d5/tengo)
runtime — the same engine as shiprig's release scripts (see
[Tengo in 5 minutes](https://github.com/rigsmith/rigsmith/blob/main/docs/TENGO-FOR-JS-DEVS.md)).
Write it as a string, an array of lines, or `{ "file": "./scripts/x.tengo" }`
(resolved relative to the config — a `.tengo` extension is the convention, and
any other is flagged as a likely typo but still loads). Tip: use backtick (raw)
string literals so they don't collide with JSON's double quotes.

**Builtins** (side effects honor `--dry-run` — previewed, not performed):

| Builtin | Does |
|---------|------|
| `sh(cmd)` | Run a shell command (portable by default; `shell:"system"` for the OS shell), return its stdout. A non-zero exit aborts the script |
| `cp` / `mv` / `rm` / `mkdir` | Cross-platform file ops (`cp -r`, `rm -rf`, `mkdir -p`) |
| `log(...)` | Print a line |
| `fail(msg)` | Abort the command with an error |

**`ctx`** exposes the invocation:

| Field | Value |
|-------|-------|
| `ctx.args` | Extra CLI args (`rig release v2` → `ctx.args[0] == "v2"`) |
| `ctx.env` | The layered environment as a map |
| `ctx.root` | Repo root |
| `ctx.cwd` | Working directory (the command's `cwd`, else root) |
| `ctx.ecosystem` | Resolved ecosystem (`dotnet`/`node`/`go`/`cargo`, or pinned) |
| `ctx.os` | `darwin` / `linux` / `windows` |

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
